package api

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Mujhtech/idenqa/internal/managedlaunch"
)

func newManagedLaunchService(path string, verifier *managedlaunch.Verifier, creator managedlaunch.SessionCreator, notices managedlaunch.NoticeReader, declare managedlaunch.AuthorityDeclarer) (*managedlaunch.Service, error) {
	file, err := os.Open(path) //nolint:gosec // operator-configured regional path, never request input
	if err != nil {
		return nil, fmt.Errorf("open managed launch preparation: %w", err)
	}
	material, readErr := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read managed launch preparation: %w", managedlaunch.ErrInvalid)
	}
	configuration, err := managedlaunch.ParsePreparationConfiguration(material)
	if err != nil {
		return nil, err
	}
	prepare, err := managedlaunch.NewPreparation(configuration, notices, declare, time.Now)
	if err != nil {
		return nil, err
	}
	return managedlaunch.NewService(verifier, creator, prepare)
}
