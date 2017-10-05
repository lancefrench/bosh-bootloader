package bosh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/cloudfoundry/bosh-bootloader/helpers"
)

type Executor struct {
	command       command
	tempDir       func(string, string) (string, error)
	readFile      func(string) ([]byte, error)
	unmarshalJSON func([]byte, interface{}) error
	marshalJSON   func(interface{}) ([]byte, error)
	writeFile     func(string, []byte, os.FileMode) error
}

type InterpolateInput struct {
	DeploymentDir          string
	VarsDir                string
	IAAS                   string
	DirectorDeploymentVars string
	JumpboxDeploymentVars  string
	BOSHState              map[string]interface{}
	Variables              string
	OpsFile                string
}

type InterpolateOutput struct {
	Variables string
	Manifest  string
}

type JumpboxInterpolateOutput struct {
	Variables string
	Manifest  string
}

type CreateEnvInput struct {
	Deployment string
	Directory  string
	Manifest   string
	Variables  string
	State      map[string]interface{}
}

type CreateEnvOutput struct {
	State map[string]interface{}
}

type DeleteEnvInput struct {
	Deployment string
	Directory  string
	Manifest   string
	Variables  string
	State      map[string]interface{}
}

type command interface {
	Run(stdout io.Writer, workingDirectory string, args []string) error
}

const VERSION_DEV_BUILD = "[DEV BUILD]"

func NewExecutor(cmd command, tempDir func(string, string) (string, error), readFile func(string) ([]byte, error),
	unmarshalJSON func([]byte, interface{}) error,
	marshalJSON func(interface{}) ([]byte, error), writeFile func(string, []byte, os.FileMode) error) Executor {
	return Executor{
		command:       cmd,
		tempDir:       tempDir,
		readFile:      readFile,
		unmarshalJSON: unmarshalJSON,
		marshalJSON:   marshalJSON,
		writeFile:     writeFile,
	}
}

func (e Executor) JumpboxInterpolate(interpolateInput InterpolateInput) (JumpboxInterpolateOutput, error) {
	tempDir, err := e.tempDir("", "")
	if err != nil {
		return JumpboxInterpolateOutput{}, fmt.Errorf("create temp dir: %s", err)
	}

	var jumpboxSetupFiles = map[string][]byte{
		"jumpbox-deployment-vars.yml": []byte(interpolateInput.JumpboxDeploymentVars),
		"jumpbox.yml":                 MustAsset("vendor/github.com/cppforlife/jumpbox-deployment/jumpbox.yml"),
	}

	if interpolateInput.IAAS == "azure" {
		jumpboxSetupFiles["cpi.yml"] = []byte(AzureJumpboxCpi)
	} else {
		jumpboxSetupFiles["cpi.yml"] = MustAsset(filepath.Join("vendor/github.com/cppforlife/jumpbox-deployment", interpolateInput.IAAS, "cpi.yml"))
	}

	if interpolateInput.Variables != "" {
		jumpboxSetupFiles["variables.yml"] = []byte(interpolateInput.Variables)
	}

	for path, contents := range jumpboxSetupFiles {
		err = e.writeFile(filepath.Join(tempDir, path), contents, os.ModePerm)
		if err != nil {
			//not tested
			return JumpboxInterpolateOutput{}, fmt.Errorf("write file: %s", err)
		}
	}

	args := []string{
		"interpolate", filepath.Join(tempDir, "jumpbox.yml"),
		"--var-errs",
		"--vars-store", filepath.Join(tempDir, "variables.yml"),
		"--vars-file", filepath.Join(tempDir, "jumpbox-deployment-vars.yml"),
		"-o", filepath.Join(tempDir, "cpi.yml"),
	}

	buffer := bytes.NewBuffer([]byte{})
	err = e.command.Run(buffer, tempDir, args)
	if err != nil {
		return JumpboxInterpolateOutput{}, fmt.Errorf("Jumpbox interpolate: %s: %s", err, buffer)
	}

	varsStore, err := e.readFile(filepath.Join(tempDir, "variables.yml"))
	if err != nil {
		return JumpboxInterpolateOutput{}, fmt.Errorf("Jumpbox read file: %s", err)
	}

	return JumpboxInterpolateOutput{
		Variables: string(varsStore),
		Manifest:  buffer.String(),
	}, nil
}

func (e Executor) DirectorInterpolate(interpolateInput InterpolateInput) (InterpolateOutput, error) {
	tempDir, err := e.tempDir("", "")
	if err != nil {
		//not tested
		return InterpolateOutput{}, err
	}

	var directorSetupFiles = map[string][]byte{
		"deployment-vars.yml":                    []byte(interpolateInput.DirectorDeploymentVars),
		"user-ops-file.yml":                      []byte(interpolateInput.OpsFile),
		"bosh.yml":                               MustAsset("vendor/github.com/cloudfoundry/bosh-deployment/bosh.yml"),
		"cpi.yml":                                MustAsset(filepath.Join("vendor/github.com/cloudfoundry/bosh-deployment", interpolateInput.IAAS, "cpi.yml")),
		"iam-instance-profile.yml":               MustAsset("vendor/github.com/cloudfoundry/bosh-deployment/aws/iam-instance-profile.yml"),
		"gcp-bosh-director-ephemeral-ip-ops.yml": []byte(GCPBoshDirectorEphemeralIPOps),
		"aws-bosh-director-ephemeral-ip-ops.yml": []byte(AWSBoshDirectorEphemeralIPOps),
		"aws-bosh-director-encrypt-disk-ops.yml": []byte(AWSEncryptDiskOps),
		"azure-ssh-static-ip.yml":                []byte(AzureSSHStaticIP),
		"jumpbox-user.yml":                       MustAsset("vendor/github.com/cloudfoundry/bosh-deployment/jumpbox-user.yml"),
		"uaa.yml":                                MustAsset("vendor/github.com/cloudfoundry/bosh-deployment/uaa.yml"),
		"credhub.yml":                            MustAsset("vendor/github.com/cloudfoundry/bosh-deployment/credhub.yml"),
	}

	if interpolateInput.Variables != "" {
		directorSetupFiles["variables.yml"] = []byte(interpolateInput.Variables)
	}

	for path, contents := range directorSetupFiles {
		err = e.writeFile(filepath.Join(tempDir, path), contents, os.ModePerm)
		if err != nil {
			//not tested
			return InterpolateOutput{}, err
		}
	}

	var args = []string{
		"interpolate", filepath.Join(tempDir, "bosh.yml"),
		"--var-errs",
		"--var-errs-unused",
		"--vars-store", filepath.Join(tempDir, "variables.yml"),
		"--vars-file", filepath.Join(tempDir, "deployment-vars.yml"),
		"-o", filepath.Join(tempDir, "cpi.yml"),
	}

	switch interpolateInput.IAAS {
	case "gcp":
		args = append(args,
			"-o", filepath.Join(tempDir, "jumpbox-user.yml"),
			"-o", filepath.Join(tempDir, "uaa.yml"),
			"-o", filepath.Join(tempDir, "credhub.yml"),
			"-o", filepath.Join(tempDir, "gcp-bosh-director-ephemeral-ip-ops.yml"),
		)
	case "aws":
		args = append(args,
			"-o", filepath.Join(tempDir, "jumpbox-user.yml"),
			"-o", filepath.Join(tempDir, "uaa.yml"),
			"-o", filepath.Join(tempDir, "credhub.yml"),
			"-o", filepath.Join(tempDir, "aws-bosh-director-ephemeral-ip-ops.yml"),
			"-o", filepath.Join(tempDir, "iam-instance-profile.yml"),
			"-o", filepath.Join(tempDir, "aws-bosh-director-encrypt-disk-ops.yml"),
		)
	case "azure":
		args = append(args,
			"-o", filepath.Join(tempDir, "jumpbox-user.yml"),
			"-o", filepath.Join(tempDir, "uaa.yml"),
			"-o", filepath.Join(tempDir, "credhub.yml"),
		)
	}

	buffer := bytes.NewBuffer([]byte{})
	err = e.command.Run(buffer, tempDir, args)
	if err != nil {
		return InterpolateOutput{}, err
	}

	if interpolateInput.OpsFile != "" {
		err = e.writeFile(filepath.Join(tempDir, "bosh.yml"), buffer.Bytes(), os.ModePerm)
		if err != nil {
			//not tested
			return InterpolateOutput{}, err
		}

		args = []string{
			"interpolate", filepath.Join(tempDir, "bosh.yml"),
			"--var-errs",
			"--vars-store", filepath.Join(tempDir, "variables.yml"),
			"--vars-file", filepath.Join(tempDir, "deployment-vars.yml"),
			"-o", filepath.Join(tempDir, "user-ops-file.yml"),
		}

		buffer = bytes.NewBuffer([]byte{})
		err = e.command.Run(buffer, tempDir, args)
		if err != nil {
			return InterpolateOutput{}, err
		}
	}

	varsStore, err := e.readFile(filepath.Join(tempDir, "variables.yml"))
	if err != nil {
		return InterpolateOutput{}, err
	}

	return InterpolateOutput{
		Variables: string(varsStore),
		Manifest:  buffer.String(),
	}, nil
}

func (e Executor) CreateEnv(createEnvInput CreateEnvInput) (CreateEnvOutput, error) {
	err := e.writePreviousFiles(createEnvInput.State, createEnvInput.Variables, createEnvInput.Manifest, createEnvInput.Directory, createEnvInput.Deployment)
	if err != nil {
		return CreateEnvOutput{}, err
	}

	statePath := filepath.Join(createEnvInput.Directory, fmt.Sprintf("%s-state.json", createEnvInput.Deployment))
	variablesPath := filepath.Join(createEnvInput.Directory, fmt.Sprintf("%s-variables.yml", createEnvInput.Deployment))
	manifestPath := filepath.Join(createEnvInput.Directory, fmt.Sprintf("%s-manifest.yml", createEnvInput.Deployment))

	args := []string{
		"create-env", manifestPath,
		"--vars-store", variablesPath,
		"--state", statePath,
	}

	err = e.command.Run(os.Stdout, createEnvInput.Directory, args)
	if err != nil {
		state, readErr := e.readBOSHState(statePath)
		if readErr != nil {
			errorList := helpers.Errors{}
			errorList.Add(err)
			errorList.Add(readErr)
			return CreateEnvOutput{}, errorList
		}

		return CreateEnvOutput{}, NewCreateEnvError(state, err)
	}

	state, err := e.readBOSHState(statePath)
	if err != nil {
		return CreateEnvOutput{}, err
	}

	return CreateEnvOutput{
		State: state,
	}, nil
}

func (e Executor) readBOSHState(statePath string) (map[string]interface{}, error) {
	stateContents, err := e.readFile(statePath)
	if err != nil {
		return map[string]interface{}{}, err
	}

	var state map[string]interface{}
	err = e.unmarshalJSON(stateContents, &state)
	if err != nil {
		return map[string]interface{}{}, err
	}

	return state, nil
}

func (e Executor) DeleteEnv(deleteEnvInput DeleteEnvInput) error {
	err := e.writePreviousFiles(deleteEnvInput.State, deleteEnvInput.Variables, deleteEnvInput.Manifest, deleteEnvInput.Directory, deleteEnvInput.Deployment)
	if err != nil {
		return err
	}

	statePath := filepath.Join(deleteEnvInput.Directory, fmt.Sprintf("%s-state.json", deleteEnvInput.Deployment))
	variablesPath := filepath.Join(deleteEnvInput.Directory, fmt.Sprintf("%s-variables.yml", deleteEnvInput.Deployment))
	manifestPath := filepath.Join(deleteEnvInput.Directory, fmt.Sprintf("%s-manifest.yml", deleteEnvInput.Deployment))

	args := []string{
		"delete-env", manifestPath,
		"--vars-store", variablesPath,
		"--state", statePath,
	}

	err = e.command.Run(os.Stdout, deleteEnvInput.Directory, args)
	if err != nil {
		state, readErr := e.readBOSHState(statePath)
		if readErr != nil {
			errorList := helpers.Errors{}
			errorList.Add(err)
			errorList.Add(readErr)
			return errorList
		}
		return NewDeleteEnvError(state, err)
	}

	return nil
}

func (e Executor) Version() (string, error) {
	tempDir, err := e.tempDir("", "")
	if err != nil {
		return "", err
	}

	args := []string{"-v"}

	buffer := bytes.NewBuffer([]byte{})
	err = e.command.Run(buffer, tempDir, args)
	if err != nil {
		return "", err
	}

	versionOutput := buffer.String()
	regex := regexp.MustCompile(`\d+.\d+.\d+`)

	version := regex.FindString(versionOutput)
	if version == "" {
		return "", NewBOSHVersionError(errors.New("BOSH version could not be parsed"))
	}

	return version, nil
}

func (e Executor) writePreviousFiles(state map[string]interface{}, variables, manifest, directory, deployment string) error {
	statePath := filepath.Join(directory, fmt.Sprintf("%s-state.json", deployment))
	variablesPath := filepath.Join(directory, fmt.Sprintf("%s-variables.yml", deployment))
	manifestPath := filepath.Join(directory, fmt.Sprintf("%s-manifest.yml", deployment))

	if state != nil {
		stateContents, err := e.marshalJSON(state)
		if err != nil {
			return err
		}
		err = e.writeFile(statePath, stateContents, os.ModePerm)
		if err != nil {
			return err
		}
	}

	err := e.writeFile(variablesPath, []byte(variables), os.ModePerm)
	if err != nil {
		// not tested
		return err
	}

	err = e.writeFile(manifestPath, []byte(manifest), os.ModePerm)
	if err != nil {
		// not tested
		return err
	}

	return nil
}
