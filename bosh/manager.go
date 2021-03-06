package bosh

import (
	"fmt"
	"os"

	yaml "gopkg.in/yaml.v2"

	"github.com/cloudfoundry/bosh-bootloader/storage"
)

var (
	osSetenv   = os.Setenv
	osUnsetenv = os.Unsetenv
)

const (
	DIRECTOR_USERNAME    = "admin"
	DIRECTOR_INTERNAL_IP = "10.0.0.6"
)

type Manager struct {
	executor    executor
	logger      logger
	socks5Proxy socks5Proxy
	stateStore  stateStore
}

type directorVars struct {
	directorPassword       string
	directorSSLCA          string
	directorSSLCertificate string
	directorSSLPrivateKey  string
}

type deploymentVariables struct {
	DirectorName          string
	Zone                  string
	Network               string
	Subnetwork            string
	Tags                  []string
	ProjectID             string
	ExternalIP            string
	CredentialsJSON       string
	PrivateKey            string
	DefaultKeyName        string
	DefaultSecurityGroups []string
	SubnetID              string
	AZ                    string
	Region                string
	SecretAccessKey       string
	AccessKeyID           string
}

type sharedDeploymentVarsYAML struct {
	InternalCIDR string    `yaml:"internal_cidr,omitempty"`
	InternalGW   string    `yaml:"internal_gw,omitempty"`
	InternalIP   string    `yaml:"internal_ip,omitempty"`
	DirectorName string    `yaml:"director_name,omitempty"`
	ExternalIP   string    `yaml:"external_ip,omitempty"`
	PrivateKey   string    `yaml:"private_key,flow,omitempty"`
	AWSYAML      AWSYAML   `yaml:",inline"`
	GCPYAML      GCPYAML   `yaml:",inline"`
	AzureYAML    AzureYAML `yaml:",inline"`
}

type AWSYAML struct {
	AZ                    string   `yaml:"az,omitempty"`
	SubnetID              string   `yaml:"subnet_id,omitempty"`
	AccessKeyID           string   `yaml:"access_key_id,omitempty"`
	SecretAccessKey       string   `yaml:"secret_access_key,omitempty"`
	IAMInstanceProfile    string   `yaml:"iam_instance_profile,omitempty"`
	DefaultKeyName        string   `yaml:"default_key_name,omitempty"`
	DefaultSecurityGroups []string `yaml:"default_security_groups,omitempty"`
	Region                string   `yaml:"region,omitempty"`
	KMSKeyARN             string   `yaml:"kms_key_arn,omitempty"`
}

type GCPYAML struct {
	Zone           string   `yaml:"zone,omitempty"`
	Network        string   `yaml:"network,omitempty"`
	Subnetwork     string   `yaml:"subnetwork,omitempty"`
	Tags           []string `yaml:"tags,omitempty"`
	ProjectID      string   `yaml:"project_id,omitempty"`
	CredentialJSON string   `yaml:"gcp_credentials_json,omitempty"`
}

type AzureYAML struct {
	VNetName             string `yaml:"vnet_name,omitempty"`
	SubnetName           string `yaml:"subnet_name,omitempty"`
	SubscriptionID       string `yaml:"subscription_id,omitempty"`
	TenantID             string `yaml:"tenant_id,omitempty"`
	ClientID             string `yaml:"client_id,omitempty"`
	ClientSecret         string `yaml:"client_secret,omitempty"`
	ResourceGroupName    string `yaml:"resource_group_name,omitempty"`
	StorageAccountName   string `yaml:"storage_account_name,omitempty"`
	DefaultSecurityGroup string `yaml:"default_security_group,omitempty"`
	PublicKey            string `yaml:"public_key,flow,omitempty"`
}

type executor interface {
	DirectorInterpolate(InterpolateInput) (InterpolateOutput, error)
	JumpboxInterpolate(InterpolateInput) (JumpboxInterpolateOutput, error)
	CreateEnv(CreateEnvInput) (CreateEnvOutput, error)
	DeleteEnv(DeleteEnvInput) error
	Version() (string, error)
}

type logger interface {
	Step(string, ...interface{})
	Println(string)
}

type socks5Proxy interface {
	Start(string, string) error
	Addr() string
}

type stateStore interface {
	GetVarsDir() (string, error)
	GetDirectorDeploymentDir() (string, error)
	GetJumpboxDeploymentDir() (string, error)
}

func NewManager(executor executor, logger logger, socks5Proxy socks5Proxy, stateStore stateStore) *Manager {
	return &Manager{
		executor:    executor,
		logger:      logger,
		socks5Proxy: socks5Proxy,
		stateStore:  stateStore,
	}
}

func (m *Manager) Version() (string, error) {
	version, err := m.executor.Version()
	switch err.(type) {
	case BOSHVersionError:
		m.logger.Println("warning: BOSH version could not be parsed")
	}
	return version, err
}

func (m *Manager) CreateJumpbox(state storage.State, terraformOutputs map[string]interface{}) (storage.State, error) {
	m.logger.Step("creating jumpbox")

	varsDir, err := m.stateStore.GetVarsDir()
	if err != nil {
		return storage.State{}, fmt.Errorf("Get vars dir: %s", err)
	}

	deploymentDir, err := m.stateStore.GetJumpboxDeploymentDir()
	if err != nil {
		return storage.State{}, fmt.Errorf("Get deployment dir: %s", err)
	}

	iaasInputs := InterpolateInput{
		DeploymentDir:  deploymentDir,
		VarsDir:        varsDir,
		IAAS:           state.IAAS,
		DeploymentVars: m.GetJumpboxDeploymentVars(state, terraformOutputs),
		Variables:      state.Jumpbox.Variables,
	}

	interpolateOutputs, err := m.executor.JumpboxInterpolate(iaasInputs)
	if err != nil {
		return storage.State{}, fmt.Errorf("Jumpbox interpolate: %s", err)
	}

	variables, err := yaml.Marshal(interpolateOutputs.Variables)
	if err != nil {
		return storage.State{}, fmt.Errorf("Marshal yaml: %s", err)
	}

	osUnsetenv("BOSH_ALL_PROXY")
	createEnvOutputs, err := m.executor.CreateEnv(CreateEnvInput{
		Deployment: "jumpbox",
		Directory:  varsDir,
		Manifest:   interpolateOutputs.Manifest,
		State:      state.Jumpbox.State,
		Variables:  string(variables),
	})
	switch err.(type) {
	case CreateEnvError:
		ceErr := err.(CreateEnvError)
		state.Jumpbox = storage.Jumpbox{
			Variables: interpolateOutputs.Variables,
			State:     ceErr.BOSHState(),
			Manifest:  interpolateOutputs.Manifest,
		}
		return storage.State{}, fmt.Errorf("Create jumpbox env: %s", NewManagerCreateError(state, err))
	case error:
		return storage.State{}, fmt.Errorf("Create jumpbox env: %s", err)
	}
	m.logger.Step("created jumpbox")

	state.Jumpbox = storage.Jumpbox{
		Variables: interpolateOutputs.Variables,
		State:     createEnvOutputs.State,
		Manifest:  interpolateOutputs.Manifest,
		URL:       terraformOutputs["jumpbox_url"].(string),
	}

	m.logger.Step("starting socks5 proxy to jumpbox")
	jumpboxPrivateKey, err := getJumpboxPrivateKey(interpolateOutputs.Variables)
	if err != nil {
		return storage.State{}, fmt.Errorf("jumpbox key: %s", err)
	}

	err = m.socks5Proxy.Start(jumpboxPrivateKey, state.Jumpbox.URL)
	if err != nil {
		return storage.State{}, fmt.Errorf("Start proxy: %s", err)
	}

	osSetenv("BOSH_ALL_PROXY", fmt.Sprintf("socks5://%s", m.socks5Proxy.Addr()))

	m.logger.Step("started proxy")
	return state, nil
}

func (m *Manager) CreateDirector(state storage.State, terraformOutputs map[string]interface{}) (storage.State, error) {
	m.logger.Step("creating bosh director")

	varsDir, err := m.stateStore.GetVarsDir()
	if err != nil {
		return storage.State{}, fmt.Errorf("Get vars dir: %s", err)
	}

	directorDeploymentDir, err := m.stateStore.GetDirectorDeploymentDir()
	if err != nil {
		return storage.State{}, fmt.Errorf("Get deployment dir: %s", err)
	}

	iaasInputs := InterpolateInput{
		DeploymentDir:  directorDeploymentDir,
		VarsDir:        varsDir,
		IAAS:           state.IAAS,
		DeploymentVars: m.GetDirectorDeploymentVars(state, terraformOutputs),
		Variables:      state.BOSH.Variables,
		OpsFile:        state.BOSH.UserOpsFile,
	}

	interpolateOutputs, err := m.executor.DirectorInterpolate(iaasInputs)
	if err != nil {
		return storage.State{}, err
	}

	createEnvOutputs, err := m.executor.CreateEnv(CreateEnvInput{
		Deployment: "director",
		Directory:  varsDir,
		Manifest:   interpolateOutputs.Manifest,
		State:      state.BOSH.State,
		Variables:  interpolateOutputs.Variables,
	})

	switch err.(type) {
	case CreateEnvError:
		ceErr := err.(CreateEnvError)
		state.BOSH = storage.BOSH{
			Variables: interpolateOutputs.Variables,
			State:     ceErr.BOSHState(),
			Manifest:  interpolateOutputs.Manifest,
		}
		return storage.State{}, NewManagerCreateError(state, err)
	case error:
		return storage.State{}, fmt.Errorf("Create director env: %s", err)
	}

	directorVars, err := getDirectorVars(interpolateOutputs.Variables)
	if err != nil {
		return storage.State{}, fmt.Errorf("Get director vars: %s", err)
	}

	state.BOSH = storage.BOSH{
		DirectorName:           fmt.Sprintf("bosh-%s", state.EnvID),
		DirectorAddress:        fmt.Sprintf("https://%s:25555", DIRECTOR_INTERNAL_IP),
		DirectorUsername:       DIRECTOR_USERNAME,
		DirectorPassword:       directorVars.directorPassword,
		DirectorSSLCA:          directorVars.directorSSLCA,
		DirectorSSLCertificate: directorVars.directorSSLCertificate,
		DirectorSSLPrivateKey:  directorVars.directorSSLPrivateKey,
		Variables:              interpolateOutputs.Variables,
		State:                  createEnvOutputs.State,
		Manifest:               interpolateOutputs.Manifest,
		UserOpsFile:            state.BOSH.UserOpsFile,
	}

	m.logger.Step("created bosh director")
	return state, nil
}

func (m *Manager) DeleteDirector(state storage.State, terraformOutputs map[string]interface{}) error {
	varsDir, err := m.stateStore.GetVarsDir()
	if err != nil {
		return fmt.Errorf("Get vars dir: %s", err)
	}

	deploymentDir, err := m.stateStore.GetDirectorDeploymentDir()
	if err != nil {
		return fmt.Errorf("Get deployment dir: %s", err)
	}

	iaasInputs := InterpolateInput{
		DeploymentDir: deploymentDir,
		VarsDir:       varsDir,
		IAAS:          state.IAAS,
		BOSHState:     state.BOSH.State,
		Variables:     state.BOSH.Variables,
		OpsFile:       state.BOSH.UserOpsFile,
	}

	jumpboxPrivateKey, err := getJumpboxPrivateKey(state.Jumpbox.Variables)
	if err != nil {
		return err
	}

	err = m.socks5Proxy.Start(jumpboxPrivateKey, state.Jumpbox.URL)
	if err != nil {
		return err
	}

	osSetenv("BOSH_ALL_PROXY", fmt.Sprintf("socks5://%s", m.socks5Proxy.Addr()))

	iaasInputs.DeploymentVars = m.GetDirectorDeploymentVars(state, terraformOutputs)

	interpolateOutputs, err := m.executor.DirectorInterpolate(iaasInputs)
	if err != nil {
		return err
	}

	err = m.executor.DeleteEnv(DeleteEnvInput{
		Deployment: "director",
		Directory:  varsDir,
		Manifest:   interpolateOutputs.Manifest,
		State:      state.BOSH.State,
		Variables:  interpolateOutputs.Variables,
	})
	switch err.(type) {
	case DeleteEnvError:
		deErr := err.(DeleteEnvError)
		state.BOSH.State = deErr.BOSHState()
		return NewManagerDeleteError(state, err)
	case error:
		return fmt.Errorf("Delete director env: %s", err)
	}

	return nil
}

func (m *Manager) DeleteJumpbox(state storage.State, terraformOutputs map[string]interface{}) error {
	m.logger.Step("destroying jumpbox")

	varsDir, err := m.stateStore.GetVarsDir()
	if err != nil {
		return fmt.Errorf("Get vars dir: %s", err)
	}

	deploymentDir, err := m.stateStore.GetJumpboxDeploymentDir()
	if err != nil {
		return fmt.Errorf("Get deployment dir: %s", err)
	}

	iaasInputs := InterpolateInput{
		DeploymentDir:  deploymentDir,
		VarsDir:        varsDir,
		IAAS:           state.IAAS,
		Variables:      state.Jumpbox.Variables,
		DeploymentVars: m.GetJumpboxDeploymentVars(state, terraformOutputs),
	}

	interpolateOutputs, err := m.executor.JumpboxInterpolate(iaasInputs)
	if err != nil {
		return err
	}

	err = m.executor.DeleteEnv(DeleteEnvInput{
		Deployment: "jumpbox",
		Directory:  varsDir,
		Manifest:   interpolateOutputs.Manifest,
		State:      state.Jumpbox.State,
		Variables:  interpolateOutputs.Variables,
	})
	switch err.(type) {
	case DeleteEnvError:
		deErr := err.(DeleteEnvError)
		state.Jumpbox.State = deErr.BOSHState()
		return NewManagerDeleteError(state, err)
	case error:
		return fmt.Errorf("Delete jumpbox env: %s", err)
	}

	return nil
}

func (m *Manager) GetJumpboxDeploymentVars(state storage.State, terraformOutputs map[string]interface{}) string {
	vars := sharedDeploymentVarsYAML{
		InternalCIDR: "10.0.0.0/24",
		InternalGW:   "10.0.0.1",
		InternalIP:   "10.0.0.5",
		DirectorName: fmt.Sprintf("bosh-%s", state.EnvID),
		ExternalIP:   getTerraformOutput("external_ip", terraformOutputs),
	}

	switch state.IAAS {
	case "gcp":
		vars.GCPYAML = GCPYAML{
			Zone:           state.GCP.Zone,
			Network:        getTerraformOutput("network_name", terraformOutputs),
			Subnetwork:     getTerraformOutput("subnetwork_name", terraformOutputs),
			Tags:           []string{getTerraformOutput("bosh_open_tag_name", terraformOutputs), getTerraformOutput("jumpbox_tag_name", terraformOutputs)},
			ProjectID:      state.GCP.ProjectID,
			CredentialJSON: state.GCP.ServiceAccountKey,
		}
	case "aws":
		vars.AWSYAML = AWSYAML{
			AZ:                    getTerraformOutput("bosh_subnet_availability_zone", terraformOutputs),
			SubnetID:              getTerraformOutput("bosh_subnet_id", terraformOutputs),
			AccessKeyID:           state.AWS.AccessKeyID,
			SecretAccessKey:       state.AWS.SecretAccessKey,
			IAMInstanceProfile:    getTerraformOutput("bosh_iam_instance_profile", terraformOutputs),
			DefaultKeyName:        getTerraformOutput("bosh_vms_key_name", terraformOutputs),
			DefaultSecurityGroups: []string{getTerraformOutput("jumpbox_security_group", terraformOutputs)},
			Region:                state.AWS.Region,
		}
		vars.PrivateKey = getTerraformOutput("bosh_vms_private_key", terraformOutputs)
	case "azure":
		vars.AzureYAML = AzureYAML{
			VNetName:             getTerraformOutput("bosh_network_name", terraformOutputs),
			SubnetName:           getTerraformOutput("bosh_subnet_name", terraformOutputs),
			SubscriptionID:       state.Azure.SubscriptionID,
			TenantID:             state.Azure.TenantID,
			ClientID:             state.Azure.ClientID,
			ClientSecret:         state.Azure.ClientSecret,
			ResourceGroupName:    getTerraformOutput("bosh_resource_group_name", terraformOutputs),
			StorageAccountName:   getTerraformOutput("bosh_storage_account_name", terraformOutputs),
			DefaultSecurityGroup: getTerraformOutput("bosh_default_security_group", terraformOutputs),
			PublicKey:            getTerraformOutput("bosh_vms_public_key", terraformOutputs),
		}
		// TODO: this struct is gross
		vars.PrivateKey = getTerraformOutput("bosh_vms_private_key", terraformOutputs)
	}

	return string(mustMarshal(vars))
}

func mustMarshal(yamlStruct interface{}) []byte {
	yamlBytes, err := yaml.Marshal(yamlStruct)
	if err != nil {
		// this should never happen since we are constructing the YAML to be marshaled
		panic("bosh manager: marshal yaml: unexpected error")
	}
	return yamlBytes
}

func getTerraformOutput(key string, outputs map[string]interface{}) string {
	if value, ok := outputs[key]; ok {
		return fmt.Sprintf("%s", value)
	}
	return ""
}

func (m *Manager) GetDirectorDeploymentVars(state storage.State, terraformOutputs map[string]interface{}) string {
	vars := sharedDeploymentVarsYAML{
		InternalCIDR: "10.0.0.0/24",
		InternalGW:   "10.0.0.1",
		InternalIP:   DIRECTOR_INTERNAL_IP,
		DirectorName: fmt.Sprintf("bosh-%s", state.EnvID),
	}

	switch state.IAAS {
	case "gcp":
		vars.GCPYAML = GCPYAML{
			Zone:           state.GCP.Zone,
			Network:        getTerraformOutput("network_name", terraformOutputs),
			Subnetwork:     getTerraformOutput("subnetwork_name", terraformOutputs),
			Tags:           []string{getTerraformOutput("bosh_director_tag_name", terraformOutputs)},
			ProjectID:      state.GCP.ProjectID,
			CredentialJSON: state.GCP.ServiceAccountKey,
		}
	case "aws":
		vars.AWSYAML = AWSYAML{
			AZ:                    getTerraformOutput("bosh_subnet_availability_zone", terraformOutputs),
			SubnetID:              getTerraformOutput("bosh_subnet_id", terraformOutputs),
			AccessKeyID:           state.AWS.AccessKeyID,
			SecretAccessKey:       state.AWS.SecretAccessKey,
			IAMInstanceProfile:    getTerraformOutput("bosh_iam_instance_profile", terraformOutputs),
			DefaultKeyName:        getTerraformOutput("bosh_vms_key_name", terraformOutputs),
			DefaultSecurityGroups: []string{getTerraformOutput("bosh_security_group", terraformOutputs)},
			Region:                state.AWS.Region,
			KMSKeyARN:             getTerraformOutput("kms_key_arn", terraformOutputs),
		}
		vars.PrivateKey = getTerraformOutput("bosh_vms_private_key", terraformOutputs)
	case "azure":
		vars.AzureYAML = AzureYAML{
			VNetName:             getTerraformOutput("bosh_network_name", terraformOutputs),
			SubnetName:           getTerraformOutput("bosh_subnet_name", terraformOutputs),
			SubscriptionID:       state.Azure.SubscriptionID,
			TenantID:             state.Azure.TenantID,
			ClientID:             state.Azure.ClientID,
			ClientSecret:         state.Azure.ClientSecret,
			ResourceGroupName:    getTerraformOutput("bosh_resource_group_name", terraformOutputs),
			StorageAccountName:   getTerraformOutput("bosh_storage_account_name", terraformOutputs),
			DefaultSecurityGroup: getTerraformOutput("bosh_default_security_group", terraformOutputs),
		}
	}

	return string(mustMarshal(vars))
}

func getJumpboxPrivateKey(v string) (string, error) {
	variables := map[string]interface{}{}

	err := yaml.Unmarshal([]byte(v), &variables)
	if err != nil {
		return "", err
	}

	jumpboxMap := variables["jumpbox_ssh"].(map[interface{}]interface{})
	jumpboxSSH := map[string]string{}
	for k, v := range jumpboxMap {
		jumpboxSSH[k.(string)] = v.(string)
	}

	return jumpboxSSH["private_key"], nil
}

func getDirectorVars(v string) (directorVars, error) {
	variables := map[string]interface{}{}

	err := yaml.Unmarshal([]byte(v), &variables)
	if err != nil {
		return directorVars{}, err
	}

	directorSSLInterfaceMap := variables["director_ssl"].(map[interface{}]interface{})
	directorSSL := map[string]string{}
	for k, v := range directorSSLInterfaceMap {
		directorSSL[k.(string)] = v.(string)
	}

	return directorVars{
		directorPassword:       variables["admin_password"].(string),
		directorSSLCA:          directorSSL["ca"],
		directorSSLCertificate: directorSSL["certificate"],
		directorSSLPrivateKey:  directorSSL["private_key"],
	}, nil
}
