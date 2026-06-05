package clusterplan

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

func Decode(reader io.Reader) (Config, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode cluster plan: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("decode cluster plan: multiple YAML documents")
	}
	return config, nil
}
