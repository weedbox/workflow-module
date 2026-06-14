# workflow-module

A weedbox / Uber Fx wrapper around [`github.com/weedbox/workflow`](https://github.com/weedbox/workflow).
It wires the workflow engine and the GORM-backed store to the injected
`database.DatabaseConnector`, auto-migrates the workflow tables on startup,
and exposes a `WorkflowManager` for other modules to inject.

## Install

```sh
go get github.com/weedbox/workflow-module
```

Requires Go 1.26+ and a `database.DatabaseConnector` provider (e.g.
`sqlite_connector` or `postgres_connector` from
`github.com/weedbox/common-modules`).

## Register the module

```go
import (
    sqlite "github.com/weedbox/common-modules/sqlite_connector"
    workflowmodule "github.com/weedbox/workflow-module"
)

func loadModules() ([]fx.Option, error) {
    return []fx.Option{
        // ... configs, logger ...
        sqlite.Module("database"),
        workflowmodule.Module("workflow"),
        // ... daemon ...
    }, nil
}
```

## Inject the manager

`WorkflowManager` is registered as a named Fx dependency. Use the `name` tag
matching the scope you passed to `Module()`.

```go
import workflowmodule "github.com/weedbox/workflow-module"

type Params struct {
    weedbox.Params
    Workflow *workflowmodule.WorkflowManager `name:"workflow"`
}

type LeaveModule struct {
    weedbox.Module[*Params]
}

func (m *LeaveModule) OnStart(ctx context.Context) error {
    tpl := workflowmodule.WorkflowTemplate{
        ID:         "leave-request",
        ReturnMode: workflowmodule.ReturnModeDirect,
        Stages: []workflowmodule.WorkflowStage{
            {StageIndex: 1, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"manager"}},
            {StageIndex: 2, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"director"}},
        },
    }
    return m.Params().Workflow.SaveTemplate(ctx, tpl)
}
```

The manager exposes everything `workflow.Service` does (`CreateDraft`,
`Submit`, `Approve`, `Return`, `Withdraw`, `RevokeApprove`, `Resubmit`,
`RejectClose`, `GetApplication`, `ListLogs`, `SaveTemplate`, `GetTemplate`)
plus accessors:

| Accessor | Returns | Use for |
|----------|---------|---------|
| `Engine()` | `*workflow.WorkflowEngine` | `CanView` / `CanReview` / `CanEditForm` / `CanResubmit` / `CanWithdraw` / `CanRevokeApprove` permission queries |
| `Store()` | `*gormstore.Store` | Low-level GORM reads, shared transactions |
| `Service()` | `*workflow.Service` | Direct service access when you need it |

## Re-exported domain types

The module re-exports the underlying library's types and constants under its
own package, so depending code does not need a separate import of
`github.com/weedbox/workflow`:

```go
workflowmodule.Application
workflowmodule.WorkflowTemplate
workflowmodule.WorkflowStage
workflowmodule.ReviewLog

workflowmodule.StatusDraft / StatusInReview / StatusReturned / StatusApproved / StatusRejectedClosed
workflowmodule.ReturnModeStrict / ReturnModeDirect
workflowmodule.ReviewTypeSingle / ReviewTypeAll
workflowmodule.ActionSubmit / ActionApprove / ActionReturn / ActionRejectClose / ActionWithdraw / ActionRevokeApprove

workflowmodule.ErrInvalidStatus / ErrNoPermission / ErrCommentRequired / ...
```

## Dynamic approvers (ApproverResolver)

The default resolver uses the static `ApproverIDs` baked into each
`WorkflowStage`. Replace it via `SetResolver` to compute reviewers at runtime
(role lookup, org chart, amount-based routing, …):

```go
m.Params().Workflow.SetResolver(workflowmodule.ApproverResolverFunc(
    func(app workflowmodule.Application, stage workflowmodule.WorkflowStage) ([]string, error) {
        return orgChart.Lookup(app.OwnerID, stage.StageIndex), nil
    },
))
```

With a custom resolver, `WorkflowStage.ApproverIDs` may be left empty. The
result must be stable for a given `(app, stage)` within a single approval
round — see the underlying library's docs for the full contract.

## Selecting a database connector

By default the module consumes the unnamed default `database.DatabaseConnector`
from the Fx graph. With `common-modules` ≥ v0.0.44 every `sqlite_connector` /
`postgres_connector` module also registers itself as a *named* provider keyed
by its scope, and the first connector loaded in the process is exposed as the
unnamed default. So:

- Single connector → default injection just works:
  ```go
  sqlite_connector.Module("db"),
  workflowmodule.Module("workflow"),  // consumes "db" as the default
  ```
- Multiple connectors → point the module at one by name:
  ```go
  sqlite_connector.Module("primary_db"),    // first-loaded → also the default
  postgres_connector.Module("analytics_db"),
  workflowmodule.Module("workflow", workflowmodule.WithDatabaseName("analytics_db")),
  ```

Multi-app tests in a single process must call
`fxmodule.ResetClaim[database.DatabaseConnector]()` between apps so the
"default connector" claim is released for the next app. See common-modules'
README for the test caveat. The tests in this module supply the connector
directly (without going through `sqlite_connector.Module`), so they do not
trigger the claim mechanism.

## Configuration

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `{scope}.auto_migrate` | `bool` | `true` | Run `gormstore.AutoMigrate` during `OnStart`. Disable when you manage migrations externally. |

```toml
[workflow]
auto_migrate = true
```

## Database schema

Tables are created by `gormstore.AutoMigrate`:

- `wf_templates`
- `wf_applications`
- `wf_review_logs`

Table names are fixed by the underlying library and not configurable.

## Authorization boundary

The library does **not** authenticate callers. `Submit`, `Resubmit`, and
`SaveTemplate` trust the caller to have already verified the requester. The
`operatorID` arg on `Approve` / `Return` / `RejectClose` is checked against
the stage's resolved approver list — that is workflow routing, not identity
verification. Wire authentication in the layer above this module.

## Tests

```sh
go test ./...
```
