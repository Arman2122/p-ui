package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

var (
	ErrFleetRuntimeCommand  = errors.New("fleet runtime command requires an owned mutation adapter")
	ErrFleetRuntimeDatabase = errors.New("fleet runtime database lifecycle changed or transaction is foreign")
)

type fleetRuntime struct {
	db   *gorm.DB
	pool *sql.DB
}

var (
	_ webruntime.Runtime             = (*fleetRuntime)(nil)
	_ webruntime.SourceEnableRuntime = (*fleetRuntime)(nil)
)

func NewFleetRuntime(db *gorm.DB) (webruntime.Runtime, error) {
	if db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || db.Error != nil || db.Name() != "postgres" || database.GetDB() != db {
		return nil, ErrFleetRuntimeDatabase
	}
	if _, transactional := db.Statement.ConnPool.(gorm.TxCommitter); transactional {
		return nil, ErrFleetRuntimeDatabase
	}
	pool, err := db.DB()
	if err != nil || pool == nil {
		return nil, errors.Join(ErrFleetRuntimeDatabase, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		return nil, errors.Join(ErrFleetRuntimeDatabase, err)
	}
	if database.GetDB() != db {
		return nil, ErrFleetRuntimeDatabase
	}
	return &fleetRuntime{db: db, pool: pool}, nil
}

func (r *fleetRuntime) checkTransaction(tx *gorm.DB) error {
	if r == nil || r.db == nil || database.GetDB() != r.db || tx == nil || tx.Config == nil || tx.Statement == nil {
		return ErrFleetRuntimeDatabase
	}
	if _, transactional := tx.Statement.ConnPool.(gorm.TxCommitter); !transactional {
		return ErrFleetRuntimeDatabase
	}
	pool, err := tx.DB()
	if err != nil || pool == nil || pool != r.pool {
		return errors.Join(ErrFleetRuntimeDatabase, err)
	}
	return tx.Error
}

func (r *fleetRuntime) SetClientEnabledTx(ctx context.Context, tx *gorm.DB, actor *model.User, request webruntime.SourceEnableRequest) (webruntime.SourceEnableResult, error) {
	if err := r.checkTransaction(tx); err != nil {
		return webruntime.SourceEnableResult{}, err
	}
	result, err := SetFleetClientEnabledTx(ctx, tx, actor, request)
	if err != nil {
		return webruntime.SourceEnableResult{}, err
	}
	if err := r.checkTransaction(tx); err != nil {
		return webruntime.SourceEnableResult{}, err
	}
	return result, nil
}

func (*fleetRuntime) Name() string { return "fleet" }

func (*fleetRuntime) AddInbound(context.Context, *model.Inbound) error {
	return fmt.Errorf("%w: AddInbound", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) DelInbound(context.Context, *model.Inbound) error {
	return fmt.Errorf("%w: DelInbound", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) UpdateInbound(context.Context, *model.Inbound, *model.Inbound) error {
	return fmt.Errorf("%w: UpdateInbound", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) AddUser(context.Context, *model.Inbound, string) error {
	return fmt.Errorf("%w: AddUser", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) RemoveUser(context.Context, *model.Inbound, string) error {
	return fmt.Errorf("%w: RemoveUser", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) CutSessions(context.Context, *model.Inbound, string, netip.Addr) (int, error) {
	return 0, fmt.Errorf("%w: CutSessions", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) UpdateUser(context.Context, *model.Inbound, string, model.Client) error {
	return fmt.Errorf("%w: UpdateUser", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) DeleteUser(context.Context, *model.Inbound, string) error {
	return fmt.Errorf("%w: DeleteUser", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) AddClient(context.Context, *model.Inbound, model.Client) error {
	return fmt.Errorf("%w: AddClient", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) DeleteClient(context.Context, string) error {
	return fmt.Errorf("%w: DeleteClient", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) RestartXray(context.Context) error {
	return fmt.Errorf("%w: RestartXray", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) ResetClientTraffic(context.Context, *model.Inbound, string) error {
	return fmt.Errorf("%w: ResetClientTraffic", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) ResetInboundTraffic(context.Context, *model.Inbound) error {
	return fmt.Errorf("%w: ResetInboundTraffic", ErrFleetRuntimeCommand)
}

func (*fleetRuntime) ResetAllTraffics(context.Context) error {
	return fmt.Errorf("%w: ResetAllTraffics", ErrFleetRuntimeCommand)
}
