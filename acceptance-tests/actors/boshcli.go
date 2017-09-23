package actors

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

type BOSHVM struct {
	ID      string   `json:"id"`
	Index   int      `json:"index"`
	State   string   `json:"job_state"`
	JobName string   `json:"job_name"`
	IPs     []string `json:"ips"`
}

type BOSHCLI struct{}

func NewBOSHCLI() BOSHCLI {
	return BOSHCLI{}
}

func (BOSHCLI) DirectorExists(address, caCertPath string) (bool, error) {
	_, err := exec.Command("bosh",
		"--ca-cert", caCertPath,
		"-e", address,
		"env",
	).Output()

	return err == nil, err
}

func (BOSHCLI) Env(address, caCertPath string) (string, error) {
	env, err := exec.Command("bosh",
		"--ca-cert", caCertPath,
		"-e", address,
		"env",
	).Output()

	return string(env), err
}

func (BOSHCLI) CloudConfig(address, caCertPath, username, password string) (string, error) {
	cloudConfig, err := exec.Command("bosh",
		"--ca-cert", caCertPath,
		"--client", username,
		"--client-secret", password,
		"-e", address,
		"cloud-config",
	).Output()

	return string(cloudConfig), err
}

func (BOSHCLI) DeleteEnv(stateFilePath, manifestPath string) error {
	_, err := exec.Command(
		"bosh",
		"delete-env",
		fmt.Sprintf("--state=%s", stateFilePath),
		manifestPath,
	).Output()

	return err
}

// TODO: use cli in test
// TODO: clean up this file

func (BOSHCLI) Deploy(address, caCertPath, username, password, deployment, manifest, varsStore string, opsFiles []string, vars map[string]string) error {
	args := []string{
		"--ca-cert", caCertPath,
		"--client", username,
		"--client-secret", password,
		"-e", address,
		"-d", deployment,
		"deploy", manifest,
		"--vars-store", varsStore,
		"-n",
	}
	for _, opsFile := range opsFiles {
		args = append(args, "-o", opsFile)
	}
	for key, value := range vars {
		args = append(args, "-v", fmt.Sprintf("%s=%s", key, value))
	}

	return exec.Command("bosh", args...).Run()
}

func (BOSHCLI) UploadRelease(address, caCertPath, username, password, releasePath string) error {
	err := exec.Command("bosh",
		"--ca-cert", caCertPath,
		"--client", username,
		"--client-secret", password,
		"-e", address,
		"upload-release", releasePath,
	).Run()
	if err != nil {
		fmt.Printf("bosh upload-release output: %s\n", string(err.(*exec.ExitError).Stderr))
	}
	return err
}

func (BOSHCLI) UploadStemcell(address, caCertPath, username, password, stemcellPath string) error {
	return exec.Command("bosh",
		"--ca-cert", caCertPath,
		"--client", username,
		"--client-secret", password,
		"-e", address,
		"upload-stemcell", stemcellPath,
	).Run()
}

func (BOSHCLI) VMs(address, caCertPath, username, password, deployment string) ([]BOSHVM, error) {
	output, err := exec.Command("bosh",
		"--ca-cert", caCertPath,
		"--client", username,
		"--client-secret", password,
		"-e", address,
		"-d", deployment,
		"vms",
		"--json",
	).Output()
	if err != nil {
		return []BOSHVM{}, err
	}
	var boshVMs []BOSHVM
	err = json.Unmarshal(output, &boshVMs)
	if err != nil {
		return []BOSHVM{}, err
	}
	return boshVMs, nil
}

func (BOSHCLI) DeleteDeployment(address, caCertPath, username, password, deployment string) error {
	return exec.Command("bosh",
		"--ca-cert", caCertPath,
		"--client", username,
		"--client-secret", password,
		"-e", address,
		"-d", deployment,
		"delete-deployment",
	).Run()
}
