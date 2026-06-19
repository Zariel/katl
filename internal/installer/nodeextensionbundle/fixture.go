package nodeextensionbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	APIVersion = "payload.katl.dev/v1alpha1"

	BundleKind   = "NodeExtensionBundle"
	IndexKind    = "NodeExtensionBundleIndex"
	CatalogKind  = "NodeExtensionBundleCatalog"
	ArtifactKind = "katl.node-app-sysext.v1"

	SysextRawMediaType    = "application/vnd.katl.sysext.raw.v1"
	PackageMediaType      = "application/vnd.katl.package-provenance.v1+json"
	CatalogEntryMediaType = "application/vnd.katl.node-extension.catalog.entry.v1+json"
)

type FixtureRequest struct {
	OutputDir         string
	AppID             string
	PayloadVersion    string
	ArtifactVersion   string
	Architecture      string
	Payload           []byte
	CreatedAt         string
	RuntimeInterfaces []string
	Capabilities      []Capability
	DisplayName       string
	Description       string
	Compatibility     *Compatibility
	Systemd           *Systemd
	Configuration     *Configuration
	Status            *Status
	Rollback          *Rollback
}

type Fixture struct {
	RootDir              string
	BundlePath           string
	BundleManifestDigest string
	IndexPath            string
	CatalogPath          string
	AppCatalogPath       string
	PayloadDigest        string
}

type Bundle struct {
	APIVersion      string `json:"apiVersion"`
	Kind            string `json:"kind"`
	AppID           string `json:"appID"`
	ArtifactKind    string `json:"artifactKind"`
	ArtifactVersion string `json:"artifactVersion"`
	PayloadVersion  string `json:"payloadVersion"`
	Architecture    string `json:"architecture"`
	DisplayName     string `json:"displayName"`
	Description     string `json:"description"`

	Capabilities  []Capability  `json:"capabilities"`
	Compatibility Compatibility `json:"compatibility"`
	Systemd       Systemd       `json:"systemd"`
	Configuration Configuration `json:"configuration"`
	Status        Status        `json:"status"`
	Rollback      Rollback      `json:"rollback"`
	Payloads      []Descriptor  `json:"payloads"`
	Metadata      []Descriptor  `json:"metadata"`
	Provenance    Provenance    `json:"provenance"`
	Signatures    []Signature   `json:"signatures,omitempty"`
}

type Capability struct {
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	ConfigSchemaIDs []string `json:"configSchemaIDs"`
	OperationKinds  []string `json:"operationKinds"`
}

type Compatibility struct {
	SupportedRuntimeInterfaces []string `json:"supportedRuntimeInterfaces"`
	RequiredKernelModules      []string `json:"requiredKernelModules"`
	RequiredUnits              []string `json:"requiredUnits"`
	RequiredMounts             []string `json:"requiredMounts"`
	RequiredCapabilities       []string `json:"requiredCapabilities"`
	ActivationPhases           []string `json:"activationPhases"`
}

type Systemd struct {
	ExtensionID          string   `json:"extensionID"`
	ExtensionVersion     string   `json:"extensionVersion"`
	SysextLevel          string   `json:"sysextLevel,omitempty"`
	ProvidedUnits        []string `json:"providedUnits"`
	EntrypointUnits      []string `json:"entrypointUnits"`
	ReadinessUnits       []string `json:"readinessUnits"`
	OrderingRequirements []string `json:"orderingRequirements"`
}

type Configuration struct {
	ConfigHandoffPaths       []string `json:"configHandoffPaths"`
	GeneratedDropInPaths     []string `json:"generatedDropInPaths"`
	SupportedConfigSchemaIDs []string `json:"supportedConfigSchemaIDs"`
	SecretRefKinds           []string `json:"secretRefKinds"`
}

type Status struct {
	LiveStatusPath      string   `json:"liveStatusPath"`
	StatusSchemaID      string   `json:"statusSchemaID"`
	DurableSnapshotPath string   `json:"durableSnapshotPath"`
	RedactionVersion    string   `json:"redactionVersion"`
	HealthStates        []string `json:"healthStates"`
}

type Rollback struct {
	FailClosedActions         []string `json:"failClosedActions"`
	LiveRollbackSupported     bool     `json:"liveRollbackSupported"`
	RequiresRebootForRollback bool     `json:"requiresRebootForRollback"`
	ExternalStateWarning      string   `json:"externalStateWarning"`
}

type Descriptor struct {
	Role      string `json:"role"`
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"sizeBytes"`
	FileName  string `json:"fileName"`
}

type Provenance struct {
	SourceRepository string `json:"sourceRepository"`
	SourceRevision   string `json:"sourceRevision"`
	BuildInputDigest string `json:"buildInputDigest"`
	CreatedAt        string `json:"createdAt"`
}

type Signature struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

type Index struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Entries    []IndexEntry `json:"entries"`
}

type IndexEntry struct {
	AppID                      string       `json:"appID"`
	PayloadVersion             string       `json:"payloadVersion"`
	ArtifactVersion            string       `json:"artifactVersion"`
	Architecture               string       `json:"architecture"`
	BundleManifestDigest       string       `json:"bundleManifestDigest"`
	BundleManifestPath         string       `json:"bundleManifestPath"`
	SysextPayloadDigest        string       `json:"sysextPayloadDigest"`
	SupportedRuntimeInterfaces []string     `json:"supportedRuntimeInterfaces"`
	Capabilities               []Capability `json:"capabilities"`
	CatalogEntryPath           string       `json:"catalogEntryPath"`
	Deprecated                 bool         `json:"deprecated"`
}

type Catalog struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	AppID      string       `json:"appID,omitempty"`
	Entries    []IndexEntry `json:"entries"`
}

type packageProvenance struct {
	APIVersion       string `json:"apiVersion"`
	Kind             string `json:"kind"`
	AppID            string `json:"appID"`
	PayloadVersion   string `json:"payloadVersion"`
	ArtifactVersion  string `json:"artifactVersion"`
	SourceRepository string `json:"sourceRepository"`
	SourceRevision   string `json:"sourceRevision"`
	BuildInputDigest string `json:"buildInputDigest"`
	CreatedAt        string `json:"createdAt"`
}

func WriteFixture(request FixtureRequest) (Fixture, error) {
	request = defaultFixtureRequest(request)
	if err := validateFixtureRequest(request); err != nil {
		return Fixture{}, err
	}

	bundleDir := filepath.Join(request.OutputDir, "bundles", request.AppID, request.PayloadVersion, request.Architecture)
	blobDir := filepath.Join(request.OutputDir, "blobs", "sha256")
	catalogDir := filepath.Join(request.OutputDir, "catalog")
	for _, dir := range []string{bundleDir, blobDir, catalogDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Fixture{}, fmt.Errorf("create node extension fixture directory: %w", err)
		}
	}

	payloadName := fmt.Sprintf("katl-node-extension-%s-%s-%s.sysext.raw", request.AppID, request.PayloadVersion, request.Architecture)
	payloadDigest, err := writeBlob(blobDir, request.Payload)
	if err != nil {
		return Fixture{}, err
	}

	provenance := packageProvenance{
		APIVersion:       APIVersion,
		Kind:             "NodeExtensionPackageProvenance",
		AppID:            request.AppID,
		PayloadVersion:   request.PayloadVersion,
		ArtifactVersion:  request.ArtifactVersion,
		SourceRepository: "fixture://katl/node-extension",
		SourceRevision:   request.ArtifactVersion,
		BuildInputDigest: payloadDigest,
		CreatedAt:        request.CreatedAt,
	}
	provenanceBytes, err := marshalCanonical(provenance)
	if err != nil {
		return Fixture{}, err
	}
	provenanceDigest, err := writeBlob(blobDir, provenanceBytes)
	if err != nil {
		return Fixture{}, err
	}
	provenancePath := filepath.Join(bundleDir, "package-provenance.json")
	if err := os.WriteFile(provenancePath, provenanceBytes, 0o644); err != nil {
		return Fixture{}, fmt.Errorf("write package provenance: %w", err)
	}

	bundlePath := filepath.ToSlash(filepath.Join("bundles", request.AppID, request.PayloadVersion, request.Architecture, "bundle.json"))
	catalogEntryPath := filepath.ToSlash(filepath.Join("bundles", request.AppID, request.PayloadVersion, request.Architecture, "catalog-entry.json"))
	catalogEntry := IndexEntry{
		AppID:                      request.AppID,
		PayloadVersion:             request.PayloadVersion,
		ArtifactVersion:            request.ArtifactVersion,
		Architecture:               request.Architecture,
		BundleManifestPath:         bundlePath,
		SysextPayloadDigest:        payloadDigest,
		SupportedRuntimeInterfaces: append([]string(nil), request.RuntimeInterfaces...),
		Capabilities:               copyCapabilities(request.Capabilities),
		CatalogEntryPath:           catalogEntryPath,
	}
	// The bundle-local catalog fragment is hashed by the bundle manifest, so it
	// cannot contain the bundle manifest digest itself. The source-root index and
	// catalogs below carry the computed bundle digest after the manifest is
	// written.
	catalogEntryBytes, err := marshalCanonical(catalogEntry)
	if err != nil {
		return Fixture{}, err
	}
	catalogEntryDigest, err := writeBlob(blobDir, catalogEntryBytes)
	if err != nil {
		return Fixture{}, err
	}

	compatibility := Compatibility{
		SupportedRuntimeInterfaces: append([]string(nil), request.RuntimeInterfaces...),
		RequiredKernelModules:      []string{},
		RequiredUnits:              []string{"systemd-sysext.service"},
		RequiredMounts:             []string{},
		RequiredCapabilities:       []string{},
		ActivationPhases:           []string{"maintenance"},
	}
	if request.Compatibility != nil {
		compatibility = copyCompatibility(*request.Compatibility)
	}
	systemd := Systemd{
		ExtensionID:          "katl-node-extension-" + request.AppID,
		ExtensionVersion:     request.PayloadVersion,
		SysextLevel:          request.RuntimeInterfaces[0],
		ProvidedUnits:        []string{"katl-app-" + request.AppID + ".service"},
		EntrypointUnits:      []string{"katl-app-" + request.AppID + ".service"},
		ReadinessUnits:       []string{"katl-app-" + request.AppID + "-ready.service"},
		OrderingRequirements: []string{"after=systemd-sysext.service"},
	}
	if request.Systemd != nil {
		systemd = copySystemd(*request.Systemd)
	}
	configuration := Configuration{
		ConfigHandoffPaths:       []string{"/etc/katl/apps/" + request.AppID + "/config.yaml"},
		GeneratedDropInPaths:     []string{"/etc/systemd/system/katl-app-" + request.AppID + ".service.d/10-katl.conf"},
		SupportedConfigSchemaIDs: configSchemaIDs(request.Capabilities),
		SecretRefKinds:           []string{},
	}
	if request.Configuration != nil {
		configuration = copyConfiguration(*request.Configuration)
	}
	status := Status{
		LiveStatusPath:      "/run/katl/apps/" + request.AppID + "/status.json",
		StatusSchemaID:      "katl.dev/node-extension-fixture-status.v1",
		DurableSnapshotPath: "/var/lib/katl/operations/<operation-id>/apps/" + request.AppID + "/status.json",
		RedactionVersion:    "v1",
		HealthStates:        []string{"unknown", "healthy", "unhealthy", "deferred"},
	}
	if request.Status != nil {
		status = copyStatus(*request.Status)
	}
	rollback := Rollback{
		FailClosedActions:         []string{},
		LiveRollbackSupported:     false,
		RequiresRebootForRollback: true,
		ExternalStateWarning:      "generic fixture has no external state",
	}
	if request.Rollback != nil {
		rollback = copyRollback(*request.Rollback)
	}
	displayName := request.DisplayName
	if displayName == "" {
		displayName = "Generic node extension fixture"
	}
	description := request.Description
	if description == "" {
		description = "Minimal generic node extension bundle fixture for delivery tests."
	}

	bundle := Bundle{
		APIVersion:      APIVersion,
		Kind:            BundleKind,
		AppID:           request.AppID,
		ArtifactKind:    ArtifactKind,
		ArtifactVersion: request.ArtifactVersion,
		PayloadVersion:  request.PayloadVersion,
		Architecture:    request.Architecture,
		DisplayName:     displayName,
		Description:     description,
		Capabilities:    copyCapabilities(request.Capabilities),
		Compatibility:   compatibility,
		Systemd:         systemd,
		Configuration:   configuration,
		Status:          status,
		Rollback:        rollback,
		Payloads: []Descriptor{{
			Role:      "systemd-sysext",
			MediaType: SysextRawMediaType,
			Digest:    payloadDigest,
			SizeBytes: int64(len(request.Payload)),
			FileName:  payloadName,
		}},
		Metadata: []Descriptor{
			{
				Role:      "package-provenance",
				MediaType: PackageMediaType,
				Digest:    provenanceDigest,
				SizeBytes: int64(len(provenanceBytes)),
				FileName:  "package-provenance.json",
			},
			{
				Role:      "catalog-fragment",
				MediaType: CatalogEntryMediaType,
				Digest:    catalogEntryDigest,
				SizeBytes: int64(len(catalogEntryBytes)),
				FileName:  "catalog-entry.json",
			},
		},
		Provenance: Provenance{
			SourceRepository: "fixture://katl/node-extension",
			SourceRevision:   request.ArtifactVersion,
			BuildInputDigest: payloadDigest,
			CreatedAt:        request.CreatedAt,
		},
		Signatures: []Signature{{
			Type:   "unsigned-fixture",
			Reason: "local or VM fixture; signature policy is deferred",
		}},
	}

	bundleBytes, err := marshalCanonical(bundle)
	if err != nil {
		return Fixture{}, err
	}
	bundleDigest, err := writeBlob(blobDir, bundleBytes)
	if err != nil {
		return Fixture{}, err
	}
	catalogEntry.BundleManifestDigest = bundleDigest

	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), bundleBytes, 0o644); err != nil {
		return Fixture{}, fmt.Errorf("write node extension bundle manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "catalog-entry.json"), catalogEntryBytes, 0o644); err != nil {
		return Fixture{}, fmt.Errorf("write node extension catalog entry: %w", err)
	}

	indexEntry := catalogEntry
	if err := writeIndex(filepath.Join(request.OutputDir, "index.json"), []IndexEntry{indexEntry}); err != nil {
		return Fixture{}, err
	}
	if err := writeCatalog(filepath.Join(catalogDir, "node-extensions.json"), "", []IndexEntry{indexEntry}); err != nil {
		return Fixture{}, err
	}
	appCatalog := filepath.Join(catalogDir, request.AppID+".json")
	if err := writeCatalog(appCatalog, request.AppID, []IndexEntry{indexEntry}); err != nil {
		return Fixture{}, err
	}
	if err := writeChecksums(request.OutputDir); err != nil {
		return Fixture{}, err
	}

	return Fixture{
		RootDir:              request.OutputDir,
		BundlePath:           filepath.Join(request.OutputDir, filepath.FromSlash(bundlePath)),
		BundleManifestDigest: bundleDigest,
		IndexPath:            filepath.Join(request.OutputDir, "index.json"),
		CatalogPath:          filepath.Join(catalogDir, "node-extensions.json"),
		AppCatalogPath:       appCatalog,
		PayloadDigest:        payloadDigest,
	}, nil
}

func defaultFixtureRequest(request FixtureRequest) FixtureRequest {
	if request.AppID == "" {
		request.AppID = "generic-fixture"
	}
	if request.PayloadVersion == "" {
		request.PayloadVersion = "generic-fixture-v0.1.0"
	}
	if request.ArtifactVersion == "" {
		request.ArtifactVersion = request.PayloadVersion + "-build.1"
	}
	if request.Architecture == "" {
		request.Architecture = "x86_64"
	}
	if len(request.Payload) == 0 {
		request.Payload = []byte("generic node extension sysext fixture\n")
	}
	if request.CreatedAt == "" {
		request.CreatedAt = "2026-06-18T00:00:00Z"
	}
	if len(request.RuntimeInterfaces) == 0 {
		request.RuntimeInterfaces = []string{"katl-runtime-1"}
	}
	if len(request.Capabilities) == 0 {
		request.Capabilities = []Capability{{
			Name:            "fixture.node-extension.delivery",
			Version:         "v1",
			ConfigSchemaIDs: []string{"katl.dev/node-extension-fixture-config.v1"},
			OperationKinds:  []string{"validate-fixture-extension"},
		}}
	}
	return request
}

func validateFixtureRequest(request FixtureRequest) error {
	if strings.TrimSpace(request.OutputDir) == "" {
		return fmt.Errorf("output directory is required")
	}
	for name, value := range map[string]string{
		"appID":           request.AppID,
		"payloadVersion":  request.PayloadVersion,
		"artifactVersion": request.ArtifactVersion,
		"architecture":    request.Architecture,
		"createdAt":       request.CreatedAt,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if strings.ContainsAny(request.AppID, `/\`) || request.AppID != strings.ToLower(request.AppID) || strings.Contains(request.AppID, "..") {
		return fmt.Errorf("appID %q must be a lower-case safe path segment", request.AppID)
	}
	if len(request.RuntimeInterfaces) == 0 {
		return fmt.Errorf("at least one runtime interface is required")
	}
	if len(request.Capabilities) == 0 {
		return fmt.Errorf("at least one capability is required")
	}
	return nil
}

func writeIndex(path string, entries []IndexEntry) error {
	sortIndexEntries(entries)
	data, err := marshalCanonical(Index{
		APIVersion: APIVersion,
		Kind:       IndexKind,
		Entries:    entries,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write node extension index: %w", err)
	}
	return nil
}

func writeCatalog(path string, appID string, entries []IndexEntry) error {
	sortIndexEntries(entries)
	data, err := marshalCanonical(Catalog{
		APIVersion: APIVersion,
		Kind:       CatalogKind,
		AppID:      appID,
		Entries:    entries,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write node extension catalog: %w", err)
	}
	return nil
}

func sortIndexEntries(entries []IndexEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].AppID != entries[j].AppID {
			return entries[i].AppID < entries[j].AppID
		}
		if entries[i].PayloadVersion != entries[j].PayloadVersion {
			return entries[i].PayloadVersion < entries[j].PayloadVersion
		}
		return entries[i].Architecture < entries[j].Architecture
	})
}

func writeChecksums(outputDir string) error {
	var lines []string
	for _, root := range []string{
		filepath.Join(outputDir, "bundles"),
		filepath.Join(outputDir, "blobs", "sha256"),
		filepath.Join(outputDir, "catalog"),
	} {
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(outputDir, path)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			lines = append(lines, fmt.Sprintf("%s  %s", hex.EncodeToString(sum[:]), filepath.ToSlash(rel)))
			return nil
		}); err != nil {
			return fmt.Errorf("walk node extension fixture files: %w", err)
		}
	}
	if err := appendChecksumLine(outputDir, "index.json", &lines); err != nil {
		return err
	}
	sort.Strings(lines)
	if err := os.WriteFile(filepath.Join(outputDir, "checksums.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write node extension checksums: %w", err)
	}
	return nil
}

func appendChecksumLine(outputDir string, rel string, lines *[]string) error {
	data, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read checksum source %s: %w", rel, err)
	}
	sum := sha256.Sum256(data)
	*lines = append(*lines, fmt.Sprintf("%s  %s", hex.EncodeToString(sum[:]), rel))
	return nil
}

func writeBlob(blobDir string, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(blobDir, digest), data, 0o644); err != nil {
		return "", fmt.Errorf("write digest-addressed node extension blob: %w", err)
	}
	return "sha256:" + digest, nil
}

func marshalCanonical(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func copyCapabilities(source []Capability) []Capability {
	out := make([]Capability, len(source))
	for i, capability := range source {
		out[i] = Capability{
			Name:            capability.Name,
			Version:         capability.Version,
			ConfigSchemaIDs: append([]string(nil), capability.ConfigSchemaIDs...),
			OperationKinds:  append([]string(nil), capability.OperationKinds...),
		}
	}
	return out
}

func copyCompatibility(source Compatibility) Compatibility {
	return Compatibility{
		SupportedRuntimeInterfaces: append([]string(nil), source.SupportedRuntimeInterfaces...),
		RequiredKernelModules:      append([]string(nil), source.RequiredKernelModules...),
		RequiredUnits:              append([]string(nil), source.RequiredUnits...),
		RequiredMounts:             append([]string(nil), source.RequiredMounts...),
		RequiredCapabilities:       append([]string(nil), source.RequiredCapabilities...),
		ActivationPhases:           append([]string(nil), source.ActivationPhases...),
	}
}

func copySystemd(source Systemd) Systemd {
	return Systemd{
		ExtensionID:          source.ExtensionID,
		ExtensionVersion:     source.ExtensionVersion,
		SysextLevel:          source.SysextLevel,
		ProvidedUnits:        append([]string(nil), source.ProvidedUnits...),
		EntrypointUnits:      append([]string(nil), source.EntrypointUnits...),
		ReadinessUnits:       append([]string(nil), source.ReadinessUnits...),
		OrderingRequirements: append([]string(nil), source.OrderingRequirements...),
	}
}

func copyConfiguration(source Configuration) Configuration {
	return Configuration{
		ConfigHandoffPaths:       append([]string(nil), source.ConfigHandoffPaths...),
		GeneratedDropInPaths:     append([]string(nil), source.GeneratedDropInPaths...),
		SupportedConfigSchemaIDs: append([]string(nil), source.SupportedConfigSchemaIDs...),
		SecretRefKinds:           append([]string(nil), source.SecretRefKinds...),
	}
}

func copyStatus(source Status) Status {
	return Status{
		LiveStatusPath:      source.LiveStatusPath,
		StatusSchemaID:      source.StatusSchemaID,
		DurableSnapshotPath: source.DurableSnapshotPath,
		RedactionVersion:    source.RedactionVersion,
		HealthStates:        append([]string(nil), source.HealthStates...),
	}
}

func copyRollback(source Rollback) Rollback {
	return Rollback{
		FailClosedActions:         append([]string(nil), source.FailClosedActions...),
		LiveRollbackSupported:     source.LiveRollbackSupported,
		RequiresRebootForRollback: source.RequiresRebootForRollback,
		ExternalStateWarning:      source.ExternalStateWarning,
	}
}

func configSchemaIDs(capabilities []Capability) []string {
	seen := map[string]struct{}{}
	var ids []string
	for _, capability := range capabilities {
		for _, id := range capability.ConfigSchemaIDs {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
