package vmtest

import (
	"context"
	"errors"
	"strings"
)

func RunKatlcSmoke(ctx context.Context, guest *GuestControl) error {
	if guest == nil {
		return errors.New("guest control is required")
	}
	if _, err := guest.RunCommand(ctx, GuestCommandRequest{Name: "katlc-binary", Argv: []string{"test", "-x", "/usr/bin/katlc"}}); err != nil {
		return err
	}
	record, err := guest.RunCommand(ctx, GuestCommandRequest{Name: "katlc-help", Argv: []string{"/usr/bin/katlc", "--help"}})
	if err != nil {
		return err
	}
	output, err := readCommandStdout(record)
	if err != nil {
		return err
	}
	if !strings.Contains(output, "Usage: katlc") || !strings.Contains(output, "operation run-tool") {
		return errors.New("katlc --help output did not include expected command summary")
	}
	return nil
}
