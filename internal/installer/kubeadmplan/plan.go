package kubeadmplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrUnsupportedManual = errors.New("unsupported kubeadm change requires manual handling")

type Classification string

const (
	NoOp                          Classification = "no-op"
	BootstrapNeeded               Classification = "bootstrap-needed"
	ExplicitReconfigureNeeded     Classification = "explicit-reconfigure-needed"
	ExplicitUpgradeNeeded         Classification = "explicit-upgrade-needed"
	UnsupportedManualIntervention Classification = "unsupported-manual"
)

type Request struct {
	FS                        fs.FS
	ConfigPath                string
	PatchesDir                string
	SelectedKubernetesVersion string
	Live                      LiveSnapshot
}

type LiveSnapshot struct {
	KubeadmConfigMap   *KubeadmConfigMapSnapshot
	KubeletConfigMap   []byte
	StaticPodManifests map[string][]byte
	KubeletConfig      []byte
	KubeadmFlagsEnv    []byte
}

type KubeadmConfigMapSnapshot struct {
	ClusterConfiguration []byte
	InitConfiguration    []byte
	JoinConfiguration    []byte
}

type Plan struct {
	Classification Classification
	Changes        []Change
	Desired        DesiredState
}

type Change struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type DesiredState struct {
	Documents                []Document
	DocumentDigests          map[string]string
	ConfigDigest             string
	PatchDigests             map[string]string
	ClusterKubernetesVersion string
	NodeCRISocket            string
	HasInitConfiguration     bool
	HasJoinConfiguration     bool
	HasClusterConfiguration  bool
	HasKubeletConfiguration  bool
}

type Document struct {
	APIVersion string
	Kind       string
}

func PlanDesiredLive(request Request) (Plan, error) {
	if request.FS == nil {
		return Plan{}, fmt.Errorf("filesystem is required")
	}
	if strings.TrimSpace(request.ConfigPath) == "" {
		return Plan{}, fmt.Errorf("config path is required")
	}
	desired, err := readDesired(request)
	if err != nil {
		if errors.Is(err, ErrUnsupportedManual) {
			return Plan{
				Classification: UnsupportedManualIntervention,
				Changes: []Change{{
					Field:   "desired",
					Message: redact(err.Error()),
				}},
			}, nil
		}
		return Plan{}, err
	}

	var changes []Change
	if request.SelectedKubernetesVersion != "" && desired.ClusterKubernetesVersion != "" && desired.ClusterKubernetesVersion != request.SelectedKubernetesVersion {
		changes = append(changes, Change{
			Field:   "selectedKubernetesVersion",
			Message: fmt.Sprintf("desired ClusterConfiguration kubernetesVersion %s does not match selected sysext %s", desired.ClusterKubernetesVersion, request.SelectedKubernetesVersion),
		})
		return Plan{Classification: ExplicitUpgradeNeeded, Changes: changes, Desired: desired}, nil
	}

	if liveMissing(request.Live) {
		return Plan{
			Classification: BootstrapNeeded,
			Changes: []Change{{
				Field:   "live",
				Message: "live kubeadm state is missing",
			}},
			Desired: desired,
		}, nil
	}

	if desired.HasClusterConfiguration {
		liveClusterVersion := documentScalar(kubeadmConfigMap(request.Live).ClusterConfiguration, "kubernetesVersion")
		if request.SelectedKubernetesVersion != "" && liveClusterVersion != "" && liveClusterVersion != request.SelectedKubernetesVersion {
			changes = append(changes, Change{
				Field:   "kubeadm-config.ClusterConfiguration.kubernetesVersion",
				Message: fmt.Sprintf("live cluster version %s does not match selected sysext %s", liveClusterVersion, request.SelectedKubernetesVersion),
			})
			return Plan{Classification: ExplicitUpgradeNeeded, Changes: changes, Desired: desired}, nil
		}
	}

	changes = append(changes, compareDesiredLive(desired, request.Live)...)
	if len(changes) == 0 {
		return Plan{Classification: NoOp, Desired: desired}, nil
	}
	return Plan{Classification: ExplicitReconfigureNeeded, Changes: changes, Desired: desired}, nil
}

func readDesired(request Request) (DesiredState, error) {
	cleanConfigPath, err := cleanFSPath(request.ConfigPath)
	if err != nil {
		return DesiredState{}, fmt.Errorf("config path: %w", err)
	}
	configData, err := fs.ReadFile(request.FS, cleanConfigPath)
	if err != nil {
		return DesiredState{}, fmt.Errorf("read desired kubeadm config: %w", err)
	}
	parsedDocuments, err := parseDocuments(configData)
	if err != nil {
		return DesiredState{}, err
	}
	if len(parsedDocuments) == 0 {
		return DesiredState{}, fmt.Errorf("desired kubeadm config must contain at least one document")
	}
	patchDigests, err := readPatchDigests(request.FS, request.PatchesDir)
	if err != nil {
		return DesiredState{}, err
	}

	state := DesiredState{
		Documents:       make([]Document, 0, len(parsedDocuments)),
		DocumentDigests: make(map[string]string, len(parsedDocuments)),
		ConfigDigest:    digest(configData),
		PatchDigests:    patchDigests,
	}
	for _, document := range parsedDocuments {
		state.Documents = append(state.Documents, document.Document)
		state.DocumentDigests[document.Kind] = digest(document.Normalized)
		switch document.Kind {
		case "InitConfiguration":
			state.HasInitConfiguration = true
			if document.NodeCRISocket != "" {
				state.NodeCRISocket = document.NodeCRISocket
			}
		case "JoinConfiguration":
			state.HasJoinConfiguration = true
			if document.NodeCRISocket != "" {
				state.NodeCRISocket = document.NodeCRISocket
			}
		case "ClusterConfiguration":
			state.HasClusterConfiguration = true
			state.ClusterKubernetesVersion = document.KubernetesVersion
		case "KubeletConfiguration":
			state.HasKubeletConfiguration = true
		}
	}
	return state, nil
}

type parsedDocument struct {
	Document
	KubernetesVersion string
	NodeCRISocket     string
	Normalized        []byte
}

func parseDocuments(data []byte) ([]parsedDocument, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var documents []parsedDocument
	for index := 0; ; index++ {
		var node yaml.Node
		err := decoder.Decode(&node)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse kubeadm YAML document %d: %w", index+1, err)
		}
		if emptyDocument(&node) {
			continue
		}
		apiVersion := mappingScalar(&node, "apiVersion")
		kind := mappingScalar(&node, "kind")
		if apiVersion == "" || kind == "" {
			return nil, fmt.Errorf("kubeadm YAML document %d requires apiVersion and kind", index+1)
		}
		if !allowedDocument(apiVersion, kind) {
			return nil, fmt.Errorf("unsupported kubeadm YAML document %d: apiVersion=%q kind=%q", index+1, apiVersion, kind)
		}
		if err := validateHostPaths(&node); err != nil {
			return nil, fmt.Errorf("kubeadm YAML document %d: %w", index+1, err)
		}
		normalized, err := normalizeYAML(&node)
		if err != nil {
			return nil, fmt.Errorf("normalize kubeadm YAML document %d: %w", index+1, err)
		}
		documents = append(documents, parsedDocument{
			Document: Document{
				APIVersion: apiVersion,
				Kind:       kind,
			},
			KubernetesVersion: mappingScalar(&node, "kubernetesVersion"),
			NodeCRISocket:     nestedScalar(&node, "nodeRegistration", "criSocket"),
			Normalized:        normalized,
		})
	}
	return documents, nil
}

func allowedDocument(apiVersion string, kind string) bool {
	switch {
	case apiVersion == "kubeadm.k8s.io/v1beta4" && (kind == "InitConfiguration" || kind == "JoinConfiguration" || kind == "ClusterConfiguration"):
		return true
	case apiVersion == "kubelet.config.k8s.io/v1beta1" && kind == "KubeletConfiguration":
		return true
	case strings.HasPrefix(apiVersion, "kubeproxy.config.k8s.io/") && kind == "KubeProxyConfiguration":
		return true
	default:
		return false
	}
}

func readPatchDigests(fsys fs.FS, patchesDir string) (map[string]string, error) {
	if strings.TrimSpace(patchesDir) == "" {
		return nil, nil
	}
	cleanDir, err := cleanFSPath(patchesDir)
	if err != nil {
		return nil, fmt.Errorf("patches dir: %w", err)
	}
	digests := map[string]string{}
	err = fs.WalkDir(fsys, cleanDir, func(name string, dirent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == cleanDir {
			return nil
		}
		if dirent.IsDir() {
			return nil
		}
		info, err := dirent.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("patch %s must be a regular file", name)
		}
		rel := strings.TrimPrefix(name, cleanDir+"/")
		if _, err := cleanFSPath(rel); err != nil {
			return fmt.Errorf("patch %s: %w", name, err)
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		if err := validatePatch(data); err != nil {
			return fmt.Errorf("patch %s: %w", name, err)
		}
		digests[rel] = digest(data)
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "file does not exist") {
			return nil, nil
		}
		return nil, err
	}
	if len(digests) == 0 {
		return nil, nil
	}
	return digests, nil
}

func compareDesiredLive(desired DesiredState, live LiveSnapshot) []Change {
	var changes []Change
	configMap := kubeadmConfigMap(live)
	desiredDocsByKind := map[string]Document{}
	for _, document := range desired.Documents {
		desiredDocsByKind[document.Kind] = document
	}
	if desired.HasClusterConfiguration && len(configMap.ClusterConfiguration) == 0 {
		changes = append(changes, Change{Field: "kubeadm-config.ClusterConfiguration", Message: "live ClusterConfiguration is missing"})
	} else if desired.HasClusterConfiguration && !documentMatches(desired.DocumentDigests["ClusterConfiguration"], configMap.ClusterConfiguration) {
		changes = append(changes, Change{Field: "kubeadm-config.ClusterConfiguration", Message: "live ClusterConfiguration differs from desired config"})
	}
	if desired.HasInitConfiguration && len(configMap.InitConfiguration) == 0 {
		changes = append(changes, Change{Field: "kubeadm-config.InitConfiguration", Message: "live InitConfiguration is missing"})
	} else if desired.HasInitConfiguration && !documentMatches(desired.DocumentDigests["InitConfiguration"], configMap.InitConfiguration) {
		changes = append(changes, Change{Field: "kubeadm-config.InitConfiguration", Message: "live InitConfiguration differs from desired config"})
	}
	if desired.HasJoinConfiguration && len(configMap.JoinConfiguration) == 0 {
		changes = append(changes, Change{Field: "kubeadm-config.JoinConfiguration", Message: "live JoinConfiguration is missing"})
	} else if desired.HasJoinConfiguration && !documentMatches(desired.DocumentDigests["JoinConfiguration"], configMap.JoinConfiguration) {
		changes = append(changes, Change{Field: "kubeadm-config.JoinConfiguration", Message: "live JoinConfiguration differs from desired config"})
	}
	if desired.HasKubeletConfiguration && len(live.KubeletConfigMap) == 0 && len(live.KubeletConfig) == 0 {
		changes = append(changes, Change{Field: "kubelet-config", Message: "live kubelet configuration is missing"})
	} else if desired.HasKubeletConfiguration {
		desiredDigest := desired.DocumentDigests["KubeletConfiguration"]
		configMapMatches := len(live.KubeletConfigMap) > 0 && documentMatches(desiredDigest, live.KubeletConfigMap)
		nodeConfigMatches := len(live.KubeletConfig) > 0 && documentMatches(desiredDigest, live.KubeletConfig)
		if !configMapMatches && !nodeConfigMatches {
			changes = append(changes, Change{Field: "kubelet-config", Message: "live kubelet configuration differs from desired config"})
		}
	}
	if len(desired.PatchDigests) > 0 {
		changes = append(changes, Change{Field: "patches", Message: "desired kubeadm patches require explicit reconfiguration review"})
	}
	if len(live.StaticPodManifests) > 0 && desiredDocsByKind["ClusterConfiguration"].Kind == "" {
		changes = append(changes, Change{Field: "staticPodManifests", Message: "live static pod manifests exist but desired ClusterConfiguration is absent"})
	}
	if len(changes) > 0 && len(live.StaticPodManifests) > 0 && desired.HasClusterConfiguration {
		changes = append(changes, Change{Field: "staticPodManifests", Message: "live static pod manifests must be reviewed before kubeadm reconfiguration"})
	}
	if len(live.KubeadmFlagsEnv) > 0 && !desired.HasInitConfiguration && !desired.HasJoinConfiguration {
		changes = append(changes, Change{Field: "kubeadm-flags.env", Message: "live kubeadm flags exist but desired node registration is absent"})
	}
	if len(live.KubeadmFlagsEnv) > 0 && desired.NodeCRISocket != "" && !bytes.Contains(live.KubeadmFlagsEnv, []byte(desired.NodeCRISocket)) {
		changes = append(changes, Change{Field: "kubeadm-flags.env", Message: "live kubeadm flags do not contain desired CRI socket"})
	}
	return changes
}

func kubeadmConfigMap(live LiveSnapshot) KubeadmConfigMapSnapshot {
	if live.KubeadmConfigMap == nil {
		return KubeadmConfigMapSnapshot{}
	}
	return *live.KubeadmConfigMap
}

func liveMissing(live LiveSnapshot) bool {
	return live.KubeadmConfigMap == nil &&
		len(live.KubeletConfigMap) == 0 &&
		len(live.StaticPodManifests) == 0 &&
		len(live.KubeletConfig) == 0 &&
		len(live.KubeadmFlagsEnv) == 0
}

func documentMatches(wantDigest string, data []byte) bool {
	got, err := normalizedDocumentDigest(data)
	return err == nil && got == wantDigest
}

func normalizedDocumentDigest(data []byte) (string, error) {
	parsed, err := parseDocuments(data)
	if err != nil {
		return "", err
	}
	if len(parsed) != 1 {
		return "", fmt.Errorf("expected one live document, got %d", len(parsed))
	}
	return digest(parsed[0].Normalized), nil
}

func documentScalar(data []byte, key string) string {
	if len(data) == 0 {
		return ""
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return ""
	}
	return mappingScalar(&node, key)
}

func validatePatch(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return fmt.Errorf("parse patch YAML: %w", err)
	}
	return validateHostPaths(&node)
}

func validateHostPaths(node *yaml.Node) error {
	for _, value := range scalarValues(node) {
		if strings.HasPrefix(value, "/") && deniedHostPath(value) {
			return fmt.Errorf("%w: host path %s", ErrUnsupportedManual, value)
		}
	}
	return nil
}

func deniedHostPath(value string) bool {
	cleaned := path.Clean(value)
	denied := []string{
		"/etc/kubernetes",
		"/usr",
		"/boot",
		"/efi",
		"/run",
		"/tmp",
		"/var/lib/katl/generations",
		"/var/lib/katl/kubernetes",
		"/var/lib/containerd",
		"/var/lib/kubelet",
	}
	for _, prefix := range denied {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return true
		}
	}
	return false
}

func scalarValues(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	var values []string
	var walk func(*yaml.Node)
	walk = func(current *yaml.Node) {
		if current.Kind == yaml.ScalarNode {
			values = append(values, current.Value)
		}
		for _, child := range current.Content {
			walk(child)
		}
	}
	walk(node)
	return values
}

func normalizeYAML(node *yaml.Node) ([]byte, error) {
	sortMappingKeys(node)
	data, err := yaml.Marshal(node)
	if err != nil {
		return nil, err
	}
	return bytes.TrimSpace(data), nil
}

func sortMappingKeys(node *yaml.Node) {
	if node == nil {
		return
	}
	for _, child := range node.Content {
		sortMappingKeys(child)
	}
	if node.Kind != yaml.MappingNode {
		return
	}
	type pair struct {
		key   *yaml.Node
		value *yaml.Node
	}
	pairs := make([]pair, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		pairs = append(pairs, pair{key: node.Content[i], value: node.Content[i+1]})
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].key.Value == pairs[j].key.Value {
			return pairs[i].key.Line < pairs[j].key.Line
		}
		return pairs[i].key.Value < pairs[j].key.Value
	})
	node.Content = node.Content[:0]
	for _, pair := range pairs {
		node.Content = append(node.Content, pair.key, pair.value)
	}
}

func emptyDocument(node *yaml.Node) bool {
	if node.Kind == 0 {
		return true
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 0 {
		return true
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 && node.Content[0].Kind == yaml.ScalarNode && node.Content[0].Value == "" {
		return true
	}
	return false
}

func mappingScalar(node *yaml.Node, key string) string {
	root := node
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key && root.Content[i+1].Kind == yaml.ScalarNode {
			return root.Content[i+1].Value
		}
	}
	return ""
}

func nestedScalar(node *yaml.Node, first string, second string) string {
	child := mappingChild(node, first)
	if child == nil {
		return ""
	}
	return mappingScalar(child, second)
}

func mappingChild(node *yaml.Node, key string) *yaml.Node {
	root := node
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}

func cleanFSPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("path is required")
	}
	cleaned := path.Clean(strings.TrimPrefix(value, "/"))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%q escapes filesystem root", value)
	}
	return cleaned, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func redact(value string) string {
	for _, word := range []string{"token", "certificate-key", "certificateKey", "password", "secret"} {
		if strings.Contains(strings.ToLower(value), strings.ToLower(word)) {
			return ErrUnsupportedManual.Error()
		}
	}
	return value
}
