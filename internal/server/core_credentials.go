package server

import (
	"errors"
	"gopkg.in/yaml.v3"
)

// The managed configuration already owns the controller credential. External
// credentials are discovered with their instance and persisted on selection.
func (p *panelServer) readManagedSecret() (string, error) {
	snapshot, err := readConfigFile(p.path)
	if err != nil {
		return "", err
	}
	if !snapshot.Exists {
		return "", errors.New("配置尚不存在")
	}
	var config struct {
		Secret string `yaml:"secret"`
	}
	if err := yaml.Unmarshal([]byte(snapshot.Content), &config); err != nil {
		return "", err
	}
	return config.Secret, nil
}
