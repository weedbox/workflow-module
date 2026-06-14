# workflow-module Development

A weedbox Method-2 module that wraps `github.com/weedbox/workflow` (approval
engine + `gormstore`) behind a `WorkflowManager`. Consumers depend only on
this package; the underlying library is re-exported via type aliases.

## Module overview

| Aspect | Value |
|--------|-------|
| Package | `workflowmodule` |
| Import | `github.com/weedbox/workflow-module` |
| Pattern | Method 2 (`weedbox.Module[*Params]`) |
| Scope | Caller-supplied via `Module("workflow")` |
| Manager type | `*workflowmodule.WorkflowManager` (named Fx dep, requires `name` tag) |
| Tables | `wf_templates`, `wf_applications`, `wf_review_logs` (fixed by library) |

## Dependencies (Params)

```go
type Params struct {
    weedbox.Params
    Database database.DatabaseConnector  // common-modules, no name tag by default
}
```

The module needs nothing else — engine and store are constructed internally
from the injected `*gorm.DB`.

### Selecting among multiple connectors

`Module(scope string, opts ...Option) fx.Option` takes functional options.
Use `WithDatabaseName("<fx-name>")` to consume a *named* `DatabaseConnector`
provider instead of the unnamed default. Internally this switches the
`fx.Invoke` from the `Params`-struct path to a positional injection with
`fx.ParamTags` so the name tag can be applied dynamically.

`common-modules` ≥ v0.0.44 makes every connector module register itself
twice: once as a named provider keyed by the connector's scope, and (only
for the first connector loaded in the process) once as the unnamed default.
That means:

- Single connector: leave `WithDatabaseName` unset; the unnamed default
  injection resolves to the only connector.
- Multiple connectors: pass `WithDatabaseName("<connector-scope>")` to pick
  a specific one. The first connector loaded still occupies the unnamed
  slot, so omitting the option points at it.

Test caveat: when an in-process test builds multiple `fx.App`s that each
load connector modules, call
`fxmodule.ResetClaim[database.DatabaseConnector]()` between apps to free
the "default connector" claim. This module's tests supply the connector
directly (bypassing `sqlite_connector.Module`) and so are not affected.

## Lifecycle

| Hook | Behaviour |
|------|-----------|
| `InitDefaultConfigs` | `viper.SetDefault({scope}.auto_migrate, true)` |
| `OnStart` | Builds `gormstore.Store`, runs `AutoMigrate` when configured, wires it into the manager via `attach()` |
| `OnStop` | No-op (DB lifecycle owned by the connector) |

The manager is **created during Fx wiring** (so other modules can inject it
in their Params), but its `store`/`service` fields stay nil until `OnStart`
runs. Calling forwarding methods before `OnStart` returns
`workflowmodule.ErrNotReady` — only call them from your own `OnStart` or
later.

## Configuration

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `{scope}.auto_migrate` | `bool` | `true` | Set to `false` to skip `gormstore.AutoMigrate` and run migrations externally |

## WorkflowManager API

### Accessors

| Method | Purpose |
|--------|---------|
| `Engine() *workflow.WorkflowEngine` | Permission queries (`CanView`, `CanReview`, `CanEditForm`, `CanResubmit`, `CanWithdraw`, `CanRevokeApprove`) |
| `Store() *gormstore.Store` | Direct GORM access, shared transactions via `Store.Transact` |
| `Service() *workflow.Service` | Full underlying service |

### Resolver

| Method | Purpose |
|--------|---------|
| `SetResolver(workflow.ApproverResolver)` | Install a dynamic approver resolver. Safe to call before OR after `OnStart`; the engine pointer never changes. |

### Forwarded `workflow.Service` methods

All of these return `ErrNotReady` if the manager has not been attached yet:

- `SaveTemplate(ctx, tpl)` — persists template and reflows in-flight applications
- `CreateDraft(ctx, app)`
- `Submit(ctx, applicationID)`
- `Approve(ctx, applicationID, operatorID)`
- `Return(ctx, applicationID, operatorID, comment)` — comment required
- `Withdraw(ctx, applicationID, comment)` — owner pulls an in-review app back to Draft
- `RevokeApprove(ctx, applicationID, operatorID, comment)` — reviewer cancels own pending approval (ALL stage, quorum still outstanding)
- `Resubmit(ctx, applicationID)`
- `RejectClose(ctx, applicationID, operatorID, comment)` — comment required
- `GetApplication(ctx, applicationID)`
- `ListLogs(ctx, applicationID)`
- `GetTemplate(ctx, templateID)` — delegates to `Store().GetTemplate`

## Re-exported types and constants (`alias.go`)

| Re-export | Underlying |
|-----------|-----------|
| `Application`, `WorkflowTemplate`, `WorkflowStage`, `ReviewLog` | type aliases |
| `ApproverResolver`, `ApproverResolverFunc` | type aliases |
| `StatusDraft`, `StatusInReview`, `StatusReturned`, `StatusApproved`, `StatusRejectedClosed` | constants |
| `ReturnModeStrict`, `ReturnModeDirect` | constants |
| `ReviewTypeSingle`, `ReviewTypeAll` | constants |
| `ActionSubmit`, `ActionApprove`, `ActionReturn`, `ActionRejectClose`, `ActionWithdraw`, `ActionRevokeApprove` | constants |
| `ErrInvalidStatus`, `ErrNoPermission`, `ErrCommentRequired`, `ErrTemplateEmpty`, `ErrTemplateMismatch`, `ErrAlreadyApproved`, `ErrInvalidTemplate`, `ErrStaleReturnStage`, `ErrNotFound`, `ErrRevokeNotAllowed`, `ErrNothingToRevoke` | error variables |
| `ErrNotReady` | **module-specific** — manager called before `OnStart` attached the store |

## Injection example

```go
type Params struct {
    weedbox.Params
    Workflow *workflowmodule.WorkflowManager `name:"workflow"`
}

type LeaveModule struct {
    weedbox.Module[*Params]
}

func (m *LeaveModule) OnStart(ctx context.Context) error {
    return m.Params().Workflow.SaveTemplate(ctx, workflowmodule.WorkflowTemplate{
        ID: "leave-request", ReturnMode: workflowmodule.ReturnModeDirect,
        Stages: []workflowmodule.WorkflowStage{
            {StageIndex: 1, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"manager"}},
            {StageIndex: 2, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"director"}},
        },
    })
}
```

## Integration notes

- **RBAC / dynamic approvers**: install a resolver via `SetResolver` from your
  own module's `OnStart` so per-application approver lists can come from
  `user-modules/rbac`, the org chart, request amount, etc.
- **Shared transactions**: use `mgr.Store().Transact(ctx, fn)` to bundle
  workflow writes with your own GORM writes atomically.
- **Permission gating**: combine `Engine().CanView(...)` with your auth
  layer's role check, e.g. `isAdmin(uid) || mgr.Engine().CanView(...)`.
- **Template updates**: `SaveTemplate` is intentionally aggressive — every call
  reflows in-flight applications (a fresh `SUBMIT` log is appended so prior
  approvals do not auto-advance). Diff upstream if you want a no-op.

## Authorization boundary

The module does NOT authenticate the caller of `Submit`, `Resubmit`, or
`SaveTemplate`. The `operatorID` arg on `Approve` / `Return` / `RejectClose`
is checked against the stage's resolved approver list (workflow routing,
not identity verification). Wire authentication in the calling layer.

## File map

| File | Purpose |
|------|---------|
| `module.go` | Method-2 Fx wiring, `Params`, `WorkflowModule`, lifecycle hooks |
| `manager.go` | `WorkflowManager`, accessors, `SetResolver`, forwarding methods |
| `alias.go` | Type aliases / constants / error re-exports |
| `module_test.go` | Fx integration tests with an in-memory sqlite stub |
