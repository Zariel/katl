package clusterplan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zariel/katl/internal/bootstrap/inventory"
	"github.com/zariel/katl/internal/installer/manifest"
)

func mergedLayer(layers ...NodeLayer) (NodeLayer, error) {
	var out NodeLayer
	for _, layer := range layers {
		var err error
		out, err = mergeLayer(out, layer)
		if err != nil {
			return NodeLayer{}, err
		}
	}
	sortNetworkd(out.Networkd.Files)
	sortExtraDisks(out.Install.ExtraDisks)
	return out, nil
}

func mergeLayer(base, next NodeLayer) (NodeLayer, error) {
	out := base
	if strings.TrimSpace(next.Hostname) != "" {
		out.Hostname = strings.TrimSpace(next.Hostname)
	}
	out.SSH.AuthorizedKeys = appendUnique(out.SSH.AuthorizedKeys, next.SSH.AuthorizedKeys)
	networkd, err := mergeNetworkd(out.Networkd, next.Networkd)
	if err != nil {
		return NodeLayer{}, err
	}
	out.Networkd = networkd
	if next.Install.TargetDisk != nil {
		disk := *next.Install.TargetDisk
		out.Install.TargetDisk = &disk
	}
	extra, err := mergeExtraDisks(out.Install.ExtraDisks, next.Install.ExtraDisks)
	if err != nil {
		return NodeLayer{}, err
	}
	out.Install.ExtraDisks = extra
	if strings.TrimSpace(next.Kubernetes.KubeadmConfigRef) != "" {
		out.Kubernetes.KubeadmConfigRef = strings.TrimSpace(next.Kubernetes.KubeadmConfigRef)
	}
	if strings.TrimSpace(next.Bootstrap.Address) != "" {
		out.Bootstrap.Address = strings.TrimSpace(next.Bootstrap.Address)
	}
	out.Bootstrap.Access = mergeAccess(out.Bootstrap.Access, next.Bootstrap.Access)
	return out, nil
}

func mergeNetworkd(base, next manifest.NetworkdConfig) (manifest.NetworkdConfig, error) {
	files := append([]manifest.NetworkdFile(nil), base.Files...)
	index := make(map[string]int, len(files))
	for i, file := range files {
		index[file.Name] = i
	}
	for _, file := range next.Files {
		if i, ok := index[file.Name]; ok {
			if files[i].Content != file.Content {
				return manifest.NetworkdConfig{}, fmt.Errorf("networkd file %q has conflicting content", file.Name)
			}
			continue
		}
		index[file.Name] = len(files)
		files = append(files, file)
	}
	sortNetworkd(files)
	return manifest.NetworkdConfig{Files: files}, nil
}

func mergeExtraDisks(base, next []manifest.ExtraDisk) ([]manifest.ExtraDisk, error) {
	disks := append([]manifest.ExtraDisk(nil), base...)
	index := make(map[string]int, len(disks))
	for i, disk := range disks {
		index[disk.Name] = i
	}
	for _, disk := range next {
		if i, ok := index[disk.Name]; ok {
			if !same(disks[i], disk) {
				return nil, fmt.Errorf("extra disk %q has conflicting settings", disk.Name)
			}
			continue
		}
		index[disk.Name] = len(disks)
		disks = append(disks, disk)
	}
	sortExtraDisks(disks)
	return disks, nil
}

func mergeAccess(base, next inventory.Access) inventory.Access {
	out := base
	if strings.TrimSpace(next.Method) != "" {
		out.Method = strings.TrimSpace(next.Method)
	}
	if strings.TrimSpace(next.User) != "" {
		out.User = strings.TrimSpace(next.User)
	}
	if strings.TrimSpace(next.CredentialRef) != "" {
		out.CredentialRef = strings.TrimSpace(next.CredentialRef)
	}
	return out
}

func appendUnique(base, next []string) []string {
	out := append([]string(nil), base...)
	seen := make(map[string]struct{}, len(out))
	for _, value := range out {
		seen[value] = struct{}{}
	}
	for _, value := range next {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortNetworkd(files []manifest.NetworkdFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
}

func sortExtraDisks(disks []manifest.ExtraDisk) {
	sort.Slice(disks, func(i, j int) bool { return disks[i].Name < disks[j].Name })
}
