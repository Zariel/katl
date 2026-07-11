package kubeadmplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"

	"gopkg.in/yaml.v3"
)

func CanonicalClusterConfigurationSHA256(data []byte) (string, error) {
	config, err := clusterConfiguration(data)
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

var profilingComponents = []string{"apiServer", "controllerManager", "scheduler"}

func SupportedControlPlaneProfilingDelta(desired, live []byte) ([]string, error) {
	desiredConfig, err := clusterConfiguration(desired)
	if err != nil {
		return nil, fmt.Errorf("desired: %w", err)
	}
	liveConfig, err := clusterConfiguration(live)
	if err != nil {
		return nil, fmt.Errorf("live: %w", err)
	}
	var delta []string
	for _, component := range profilingComponents {
		desiredValue, desiredSet, err := removeProfiling(desiredConfig, component)
		if err != nil {
			return nil, err
		}
		liveValue, liveSet, err := removeProfiling(liveConfig, component)
		if err != nil {
			return nil, err
		}
		if liveSet || !desiredSet {
			if liveSet != desiredSet || liveValue != desiredValue {
				return nil, fmt.Errorf("unsupported profiling transition for %s", component)
			}
			continue
		}
		if desiredValue != "false" {
			return nil, fmt.Errorf("%s profiling value must be false", component)
		}
		delta = append(delta, "ClusterConfiguration."+component+".extraArgs.profiling=false")
	}
	if !reflect.DeepEqual(desiredConfig, liveConfig) {
		return nil, fmt.Errorf("unsupported ClusterConfiguration difference outside profiling=false")
	}
	sort.Strings(delta)
	if len(delta) == 0 {
		return nil, fmt.Errorf("no supported profiling=false additions")
	}
	return delta, nil
}

func clusterConfiguration(data []byte) (map[string]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if doc["apiVersion"] == "kubeadm.k8s.io/v1beta4" && doc["kind"] == "ClusterConfiguration" {
			return doc, nil
		}
	}
	return nil, fmt.Errorf("kubeadm.k8s.io/v1beta4 ClusterConfiguration is required")
}

func removeProfiling(config map[string]any, component string) (string, bool, error) {
	value, ok := config[component]
	if !ok {
		return "", false, nil
	}
	section, ok := value.(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("%s must be a mapping", component)
	}
	raw, ok := section["extraArgs"]
	if !ok {
		if len(section) == 0 {
			delete(config, component)
		}
		return "", false, nil
	}
	args, ok := raw.([]any)
	if !ok {
		return "", false, fmt.Errorf("%s.extraArgs must be a list", component)
	}
	filtered := make([]any, 0, len(args))
	found := ""
	for _, rawArg := range args {
		arg, ok := rawArg.(map[string]any)
		if !ok {
			return "", false, fmt.Errorf("%s.extraArgs entry must be a mapping", component)
		}
		if arg["name"] == "profiling" {
			if found != "" {
				return "", false, fmt.Errorf("%s profiling argument is repeated", component)
			}
			text, ok := arg["value"].(string)
			if !ok {
				return "", false, fmt.Errorf("%s profiling value must be a string", component)
			}
			found = text
			continue
		}
		filtered = append(filtered, arg)
	}
	if len(filtered) == 0 {
		delete(section, "extraArgs")
	} else {
		section["extraArgs"] = filtered
	}
	if len(section) == 0 {
		delete(config, component)
	}
	return found, found != "", nil
}
