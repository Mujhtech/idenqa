package idenqa

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/transport/localreadinessbridge"
)

const (
	localReadinessAPIVersion = "1.0.0-alpha.1"
	localReadinessURIMajor   = 1
	localReadinessTimeout    = 2 * time.Second
)

type localReadinessService struct {
	configuration config.API
	database      *postgres.Pool
}

func (service localReadinessService) Snapshot(ctx context.Context) (localreadinessbridge.Snapshot, error) {
	checks := []localreadinessbridge.Check{{Name: "configuration", Status: "pass"}}
	status := func(err error) string {
		if err != nil {
			return "fail"
		}
		return "pass"
	}
	checks = append(checks, localreadinessbridge.Check{Name: "database", Status: status(service.database.Ping(ctx))})
	checks = append(checks, localreadinessbridge.Check{Name: "migrations", Status: status(service.database.Check(ctx, migrations.LatestVersion))})
	headgate, headgateErr := taskheadgate.CheckPoolSchema(ctx, service.database.Native(), service.configuration.HeadgateSchema)
	if headgateErr == nil && (headgate.Pending || headgate.State == "empty" || headgate.State == "unversioned" || headgate.Current > headgate.Latest) {
		headgateErr = errors.New("headgate schema is not current")
	}
	checks = append(checks, localreadinessbridge.Check{Name: "headgate", Status: status(headgateErr)})
	checks = append(checks, localreadinessbridge.Check{Name: "api-reachability", Status: status(probeLocalAPI(ctx, service.configuration.HTTPHost, service.configuration.HTTPPort))})
	return localreadinessbridge.Snapshot{
		Version: localreadinessbridge.ContractVersion, CoreRevision: buildinfo.Current().Commit,
		APIVersion: localReadinessAPIVersion, URIMajor: localReadinessURIMajor, Checks: checks,
	}, nil
}

func probeLocalAPI(ctx context.Context, host string, port uint16) error {
	switch strings.TrimSpace(host) {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	probeCtx, cancel := context.WithTimeout(ctx, localReadinessTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return err
	}
	return connection.Close()
}
