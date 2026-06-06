//go:build linux

package vmtest

import "golang.org/x/sys/unix"

func noFollowFlag() int {
	return unix.O_NOFOLLOW
}
