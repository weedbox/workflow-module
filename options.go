package workflowmodule

// Option configures the workflow module at registration time.
type Option func(*options)

type options struct {
	// databaseName, when non-empty, asks Fx to inject the database connector
	// registered under that name (`fx.Annotated{Name: ...}`) instead of the
	// unnamed default. Required when the host app registers more than one
	// database.DatabaseConnector and needs to point the workflow module at a
	// specific instance.
	databaseName string
}

func defaultOptions() *options {
	return &options{}
}

// WithDatabaseName selects which named database.DatabaseConnector this module
// should consume. The name must match the one used on the provider, e.g.
//
//	fx.Provide(fx.Annotated{Name: "workflow_db", Target: func() database.DatabaseConnector { ... }})
//	workflowmodule.Module("workflow", workflowmodule.WithDatabaseName("workflow_db"))
//
// Leave unset to inject the unnamed connector (the default and only mode
// supported by common-modules' built-in sqlite/postgres connectors out of
// the box).
func WithDatabaseName(name string) Option {
	return func(o *options) { o.databaseName = name }
}
