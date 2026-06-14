// Package workflowmodule wraps github.com/weedbox/workflow as a weedbox/Fx
// module. It wires the engine and the GORM-backed store to the injected
// database.DatabaseConnector, auto-migrates the workflow tables on startup,
// and exposes a WorkflowManager that callers inject into their own modules.
package workflowmodule

import (
	"context"

	"github.com/spf13/viper"
	"github.com/weedbox/common-modules/database"
	"github.com/weedbox/weedbox"
	"github.com/weedbox/workflow"
	"github.com/weedbox/workflow/gormstore"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const ModuleName = "WorkflowModule"

// Params declares the module's Fx dependencies. The database is injected as
// the common-modules interface so any connector (sqlite, postgres, ...) works.
type Params struct {
	weedbox.Params

	Database database.DatabaseConnector
}

// WorkflowModule is the weedbox module wrapper. The actual user-facing API
// lives on the embedded WorkflowManager.
type WorkflowModule struct {
	weedbox.Module[*Params]

	manager *WorkflowManager
}

// Module returns the Fx option that registers the workflow module under scope.
// The manager is exported as a named dependency (`name:"<scope>"`) so other
// Method-2 modules can inject it.
//
// Pass WithDatabaseName to pick a specific named database.DatabaseConnector
// when the host app registers more than one.
func Module(scope string, opts ...Option) fx.Option {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	m := new(WorkflowModule)

	return fx.Module(
		scope,
		fx.Supply(
			fx.Annotated{
				Name:   scope,
				Target: m,
			},
		),
		fx.Provide(
			fx.Annotated{
				Name: scope,
				Target: func() *WorkflowManager {
					// Stable pointer: callers grab the manager during Fx wiring
					// (via fx.In) and store it on their own modules. OnStart
					// wires the store/service into the same instance.
					if m.manager == nil {
						m.manager = newManager()
					}
					return m.manager
				},
			},
		),
		buildInvoke(scope, m, o),
	)
}

// buildInvoke chooses between unnamed and named database injection. The
// unnamed path uses the Params struct so embedded weedbox.Params (Lifecycle,
// Logger) flows through normally. The named path injects the three pieces
// individually and reassembles them, because the `name` tag has to live on a
// field, not on a struct, and we cannot make it dynamic at compile time.
func buildInvoke(scope string, m *WorkflowModule, o *options) fx.Option {
	if o.databaseName == "" {
		return fx.Invoke(func(p Params) {
			weedbox.InitModule(scope, &p, m)
		})
	}

	return fx.Invoke(fx.Annotate(
		func(lc fx.Lifecycle, logger *zap.Logger, db database.DatabaseConnector) {
			p := &Params{
				Params: weedbox.Params{
					Lifecycle: lc,
					Logger:    logger,
				},
				Database: db,
			}
			weedbox.InitModule(scope, p, m)
		},
		// fx.ParamTags applies positionally to each parameter: lifecycle and
		// logger keep their default (unnamed) lookup; the database is fetched
		// from the named provider the caller selected.
		fx.ParamTags(``, ``, `name:"`+o.databaseName+`"`),
	))
}

// Manager returns the WorkflowManager owned by this module.
func (m *WorkflowModule) Manager() *WorkflowManager {
	if m.manager == nil {
		m.manager = newManager()
	}
	return m.manager
}

// OnStart builds the GORM store, runs AutoMigrate (when configured), and wires
// the engine/store into the manager.
func (m *WorkflowModule) OnStart(ctx context.Context) error {
	m.Logger().Info(ModuleName + " starting")

	db := m.Params().Database.GetDB()
	store := gormstore.New(db)

	if viper.GetBool(m.GetConfigPath("auto_migrate")) {
		if err := store.AutoMigrate(); err != nil {
			return err
		}
	}

	mgr := m.Manager()
	mgr.attach(store)

	m.Logger().Info(ModuleName + " started")
	return nil
}

// OnStop is a no-op; the underlying *gorm.DB is owned by the connector module.
func (m *WorkflowModule) OnStop(ctx context.Context) error {
	m.Logger().Info(ModuleName + " stopped")
	return nil
}

// InitDefaultConfigs registers Viper defaults for this module's scope.
func (m *WorkflowModule) InitDefaultConfigs() {
	viper.SetDefault(m.GetConfigPath("auto_migrate"), true)
}

// newManager builds an empty manager wired to a default engine. The store is
// attached during OnStart once the database is available.
func newManager() *WorkflowManager {
	engine := workflow.NewEngine()
	return &WorkflowManager{engine: engine}
}
