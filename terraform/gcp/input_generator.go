package gcp

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/cloudfoundry/bosh-bootloader/storage"
)

var tempDir func(dir, prefix string) (string, error) = ioutil.TempDir
var writeFile func(file string, data []byte, perm os.FileMode) error = ioutil.WriteFile

type InputGenerator struct {
}

func NewInputGenerator() InputGenerator {
	return InputGenerator{}
}

func (i InputGenerator) Generate(state storage.State) (map[string]string, error) {
	dir, err := tempDir("", "")
	if err != nil {
		return map[string]string{}, err
	}

	credentialsPath := filepath.Join(dir, "credentials.json")
	err = writeFile(credentialsPath, []byte(state.GCP.ServiceAccountKey), os.ModePerm)
	if err != nil {
		return map[string]string{}, err
	}

	input := map[string]string{
		"env_id":        state.EnvID,
		"project_id":    state.GCP.ProjectID,
		"region":        state.GCP.Region,
		"zone":          state.GCP.Zone,
		"credentials":   credentialsPath,
		"system_domain": state.LB.Domain,
	}

	if state.LB.Cert != "" && state.LB.Key != "" {
		certPath := filepath.Join(dir, "cert")
		err = writeFile(certPath, []byte(state.LB.Cert), os.ModePerm)
		if err != nil {
			return map[string]string{}, err
		}
		input["ssl_certificate"] = certPath

		keyPath := filepath.Join(dir, "key")
		err = writeFile(keyPath, []byte(state.LB.Key), os.ModePerm)
		if err != nil {
			return map[string]string{}, err
		}
		input["ssl_certificate_private_key"] = keyPath
	}

	return input, nil
}
