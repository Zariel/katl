package resourcetest

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

func ParseRPMPackages(r io.Reader) ([]Package, error) {
	var packages []Package
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			return nil, fmt.Errorf("rpm package line must contain name and NEVRA separated by a tab: %q", line)
		}
		pkg := Package{
			Name:  strings.TrimSpace(fields[0]),
			NEVRA: strings.TrimSpace(fields[1]),
		}
		if pkg.Name == "" || pkg.NEVRA == "" {
			return nil, fmt.Errorf("rpm package line has empty name or NEVRA: %q", line)
		}
		packages = append(packages, pkg)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Name == packages[j].Name {
			return packages[i].NEVRA < packages[j].NEVRA
		}
		return packages[i].Name < packages[j].Name
	})
	return packages, nil
}
