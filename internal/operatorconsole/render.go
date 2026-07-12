package operatorconsole

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func Render(snapshot Snapshot, width, height int) string {
	if width < 40 {
		width = 40
	}
	if height < 12 {
		height = 12
	}
	lines := make([]string, 0, height)
	title := "KatlOS"
	if snapshot.Mode == ModeInstaller {
		title += " Installer"
	}
	if snapshot.Version != "" {
		title += "  " + snapshot.Version
	}
	lines = append(lines, title, strings.Repeat("=", min(width, 72)))
	lines = append(lines, field("State", stateLabel(snapshot.State)))
	if snapshot.Hostname != "" {
		lines = append(lines, field("Node", snapshot.Hostname))
	}
	lines = append(lines, networkLines(snapshot.Network)...)
	if snapshot.CurrentStep != "" {
		lines = append(lines, field("Current step", snapshot.CurrentStep))
	}
	if snapshot.TargetDisk != "" {
		lines = append(lines, field("Target disk", snapshot.TargetDisk))
	}
	if snapshot.Generation != "" {
		value := snapshot.Generation
		if snapshot.GenerationBoot != "" || snapshot.GenerationHealth != "" {
			value += fmt.Sprintf("  boot=%s health=%s", fallback(snapshot.GenerationBoot, "unknown"), fallback(snapshot.GenerationHealth, "unknown"))
		}
		lines = append(lines, field("Generation", value))
	}
	if snapshot.Mode == ModeInstaller && snapshot.State == "running" {
		mutation := "not started"
		if snapshot.DestructiveMutation {
			mutation = "started - do not power off"
		}
		lines = append(lines, field("Disk changes", mutation))
	}
	if snapshot.Handoff.URL != "" {
		lines = append(lines, field("Configure", snapshot.Handoff.URL), field("Token", snapshot.Handoff.Token))
		lines = append(lines, "From another machine use: katlctl install apply")
	}
	if snapshot.LastError != "" {
		lines = append(lines, wrapField("Error", snapshot.LastError, width)...)
	}
	if snapshot.RetryHint != "" {
		lines = append(lines, wrapField("Next action", snapshot.RetryHint, width)...)
	}
	if snapshot.StatusError != "" {
		lines = append(lines, wrapField("Status read", snapshot.StatusError, width)...)
	}

	footer := "Ctrl+Alt+F2: local console"
	if snapshot.SSHEnabled {
		if address := firstIPv4(snapshot.Network); address != "" {
			footer += " | SSH: ssh root@" + address
		} else {
			footer += " | SSH enabled"
		}
	} else if snapshot.Mode == ModeInstaller {
		footer += " | SSH disabled by installer config"
	}
	journalRows := height - len(lines) - 3
	if journalRows < 2 {
		journalRows = 2
	}
	lines = append(lines, "", "Journal (live)")
	journal := snapshot.Journal
	if len(journal) > journalRows {
		journal = journal[len(journal)-journalRows:]
	}
	for _, line := range journal {
		lines = append(lines, truncate(sanitize(line), width))
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	if len(lines) >= height {
		lines = lines[:height-1]
	}
	lines = append(lines, truncate(footer, width))
	for i := range lines {
		lines[i] = truncate(lines[i], width)
	}
	return strings.Join(lines, "\n") + "\n"
}

func networkLines(network []NetworkInterface) []string {
	if len(network) == 0 {
		return []string{field("Network", "waiting for an active interface")}
	}
	lines := make([]string, 0, len(network))
	for i, iface := range network {
		value := iface.Name + ": configuring"
		if len(iface.Addresses) > 0 {
			value = iface.Name + ": " + strings.Join(iface.Addresses, ", ")
		}
		label := ""
		if i == 0 {
			label = "Network"
		}
		lines = append(lines, field(label, value))
	}
	return lines
}

func stateLabel(state string) string {
	labels := map[string]string{
		"starting-installer":            "Starting installer",
		"starting-runtime":              "Starting installed system",
		"running":                       "Installing",
		"debug-hold":                    "Debug hold; installation disabled",
		"waiting-for-config":            "Waiting for configuration",
		"install-refused":               "Installation refused",
		"failed-before-mutation":        "Installation failed; disk unchanged",
		"failed-after-mutation":         "Installation failed; repair required",
		"reboot-requested":              "Installation complete; rebooting",
		"kubeadm-ready":                 "Ready for Kubernetes bootstrap",
		"waiting-for-cluster-bootstrap": "Waiting for Kubernetes bootstrap",
		"runtime-booted-not-ready":      "Installed system booted; not ready",
		"runtime-failed-needs-repair":   "Installed system needs repair",
	}
	if label := labels[state]; label != "" {
		return label
	}
	return fallback(state, "Unknown")
}

func field(label, value string) string {
	if label == "" {
		return fmt.Sprintf("%-14s%s", "", value)
	}
	return fmt.Sprintf("%-14s%s", label+":", value)
}

func wrapField(label, value string, width int) []string {
	prefix := field(label, "")
	available := width - utf8.RuneCountInString(prefix)
	if available < 10 {
		available = 10
	}
	words := strings.Fields(sanitize(value))
	if len(words) == 0 {
		return []string{prefix}
	}
	var lines []string
	current := ""
	for _, word := range words {
		if current != "" && utf8.RuneCountInString(current)+1+utf8.RuneCountInString(word) > available {
			lines = append(lines, prefix+current)
			prefix = strings.Repeat(" ", utf8.RuneCountInString(prefix))
			current = word
			continue
		}
		if current != "" {
			current += " "
		}
		current += word
	}
	return append(lines, prefix+current)
}

func sanitize(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width < 2 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "~"
}

func firstIPv4(network []NetworkInterface) string {
	for _, iface := range network {
		for _, address := range iface.Addresses {
			if strings.Contains(address, ".") {
				return strings.SplitN(address, "/", 2)[0]
			}
		}
	}
	return ""
}

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
