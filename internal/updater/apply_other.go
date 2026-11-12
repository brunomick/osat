//go:build !windows && !darwin && !(linux && !android)

package updater

import (
	"context"

	"osat/internal/i18n"
)

// ErrNotWritable: en este sistema (Android, por ejemplo) osat no se actualiza solo.
var ErrNotWritable = i18n.Err("update.notHere")

func Detect() (Kind, error) { return "", ErrNotWritable }

func Apply(context.Context, Release, string, Progress) (Relaunch, error) { return nil, ErrNotWritable }
