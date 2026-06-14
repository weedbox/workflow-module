package workflowmodule_test

import (
	"context"
	"testing"
	"time"

	"github.com/weedbox/common-modules/database"
	workflowmodule "github.com/weedbox/workflow-module"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// stubDB satisfies database.DatabaseConnector for tests without bringing in
// the full sqlite_connector module (which requires viper config and a file
// path on disk).
type stubDB struct{ db *gorm.DB }

func (s *stubDB) GetDB() *gorm.DB { return s.db }

func newStubDB(t *testing.T) database.DatabaseConnector {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return &stubDB{db: db}
}

// startApp wires the workflow module into an Fx app, extracts the manager
// via fx.Populate, and returns both the app and the manager.
func startApp(t *testing.T) (*fxtest.App, *workflowmodule.WorkflowManager) {
	t.Helper()

	var manager *workflowmodule.WorkflowManager

	conn := newStubDB(t)
	app := fxtest.New(t,
		fx.NopLogger,
		fx.Provide(func() *zap.Logger { return zap.NewNop() }),
		fx.Provide(func() database.DatabaseConnector { return conn }),
		workflowmodule.Module("workflow"),
		fx.Populate(fx.Annotate(&manager, fx.ParamTags(`name:"workflow"`))),
	)
	app.RequireStart()
	t.Cleanup(func() { app.RequireStop() })

	if manager == nil {
		t.Fatal("manager was not populated")
	}
	return app, manager
}

func TestFullFlow_SingleSignTwoStages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, mgr := startApp(t)

	tpl := workflowmodule.WorkflowTemplate{
		ID:         "leave-request",
		ReturnMode: workflowmodule.ReturnModeDirect,
		Stages: []workflowmodule.WorkflowStage{
			{StageIndex: 1, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"manager"}},
			{StageIndex: 2, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"director"}},
		},
	}
	if err := mgr.SaveTemplate(ctx, tpl); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}

	app := workflowmodule.Application{ID: "leave-001", WorkflowID: tpl.ID, OwnerID: "alice"}
	if err := mgr.CreateDraft(ctx, app); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	if _, _, err := mgr.Submit(ctx, app.ID); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, _, err := mgr.Approve(ctx, app.ID, "manager"); err != nil {
		t.Fatalf("Approve manager: %v", err)
	}
	final, _, err := mgr.Approve(ctx, app.ID, "director")
	if err != nil {
		t.Fatalf("Approve director: %v", err)
	}
	if final.Status != workflowmodule.StatusApproved {
		t.Fatalf("final status = %q, want Approved", final.Status)
	}

	logs, err := mgr.ListLogs(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	// Submit + 2 approvals = 3 entries.
	if len(logs) != 3 {
		t.Fatalf("log count = %d, want 3", len(logs))
	}
}

func TestReturnAndResubmit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, mgr := startApp(t)

	tpl := workflowmodule.WorkflowTemplate{
		ID:         "expense",
		ReturnMode: workflowmodule.ReturnModeDirect,
		Stages: []workflowmodule.WorkflowStage{
			{StageIndex: 1, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"manager"}},
			{StageIndex: 2, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"director"}},
		},
	}
	if err := mgr.SaveTemplate(ctx, tpl); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}

	app := workflowmodule.Application{ID: "exp-001", WorkflowID: tpl.ID, OwnerID: "bob"}
	if err := mgr.CreateDraft(ctx, app); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, _, err := mgr.Submit(ctx, app.ID); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, _, err := mgr.Approve(ctx, app.ID, "manager"); err != nil {
		t.Fatalf("Approve stage 1: %v", err)
	}

	// Director returns from stage 2; DIRECT mode should resume at stage 2.
	returned, _, err := mgr.Return(ctx, app.ID, "director", "missing receipts")
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if returned.Status != workflowmodule.StatusReturned || returned.ReturnStageIndex != 2 {
		t.Fatalf("unexpected returned state: status=%s returnStage=%d", returned.Status, returned.ReturnStageIndex)
	}

	resubmitted, _, err := mgr.Resubmit(ctx, app.ID)
	if err != nil {
		t.Fatalf("Resubmit: %v", err)
	}
	if resubmitted.Status != workflowmodule.StatusInReview || resubmitted.CurrentStageIndex != 2 {
		t.Fatalf("unexpected resubmit state: status=%s stage=%d", resubmitted.Status, resubmitted.CurrentStageIndex)
	}
}

func TestWithdraw_OwnerPullsBackToDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, mgr := startApp(t)

	tpl := workflowmodule.WorkflowTemplate{
		ID:         "withdrawable",
		ReturnMode: workflowmodule.ReturnModeDirect,
		Stages: []workflowmodule.WorkflowStage{
			{StageIndex: 1, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"manager"}},
		},
	}
	if err := mgr.SaveTemplate(ctx, tpl); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}

	app := workflowmodule.Application{ID: "wd-001", WorkflowID: tpl.ID, OwnerID: "alice"}
	if err := mgr.CreateDraft(ctx, app); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, _, err := mgr.Submit(ctx, app.ID); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Owner withdraws the in-review application back to Draft.
	withdrawn, log, err := mgr.Withdraw(ctx, app.ID, "changed my mind")
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if withdrawn.Status != workflowmodule.StatusDraft {
		t.Fatalf("status after withdraw = %q, want Draft", withdrawn.Status)
	}
	if log.Action != workflowmodule.ActionWithdraw {
		t.Fatalf("log action = %q, want %q", log.Action, workflowmodule.ActionWithdraw)
	}
}

func TestRevokeApprove_AllStageQuorumOutstanding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, mgr := startApp(t)

	// A single ALL (co-sign) stage requiring both approvers.
	tpl := workflowmodule.WorkflowTemplate{
		ID:         "cosign",
		ReturnMode: workflowmodule.ReturnModeStrict,
		Stages: []workflowmodule.WorkflowStage{
			{StageIndex: 1, ReviewType: workflowmodule.ReviewTypeAll, ApproverIDs: []string{"a", "b"}},
		},
	}
	if err := mgr.SaveTemplate(ctx, tpl); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}

	app := workflowmodule.Application{ID: "cs-001", WorkflowID: tpl.ID, OwnerID: "dave"}
	if err := mgr.CreateDraft(ctx, app); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, _, err := mgr.Submit(ctx, app.ID); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// "a" approves; quorum (2 of 2) is still outstanding, so it stays in review.
	mid, _, err := mgr.Approve(ctx, app.ID, "a")
	if err != nil {
		t.Fatalf("Approve a: %v", err)
	}
	if mid.Status != workflowmodule.StatusInReview {
		t.Fatalf("status after first approve = %q, want In_Review", mid.Status)
	}

	// "a" revokes their own still-pending approval.
	revoked, log, err := mgr.RevokeApprove(ctx, app.ID, "a", "made a mistake")
	if err != nil {
		t.Fatalf("RevokeApprove: %v", err)
	}
	if revoked.Status != workflowmodule.StatusInReview || revoked.CurrentStageIndex != 1 {
		t.Fatalf("unexpected state after revoke: status=%s stage=%d", revoked.Status, revoked.CurrentStageIndex)
	}
	if log.Action != workflowmodule.ActionRevokeApprove {
		t.Fatalf("log action = %q, want %q", log.Action, workflowmodule.ActionRevokeApprove)
	}

	// After revoking, "a" can approve again and then "b" completes the quorum.
	if _, _, err := mgr.Approve(ctx, app.ID, "a"); err != nil {
		t.Fatalf("re-approve a: %v", err)
	}
	final, _, err := mgr.Approve(ctx, app.ID, "b")
	if err != nil {
		t.Fatalf("Approve b: %v", err)
	}
	if final.Status != workflowmodule.StatusApproved {
		t.Fatalf("final status = %q, want Approved", final.Status)
	}
}

func TestWithDatabaseName_SelectsNamedConnector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Two distinct in-memory connectors registered under different names. The
	// workflow module must consume the one we point it at; rows written via
	// the manager must show up in that DB and NOT in the other one.
	primary := newStubDB(t)
	secondary := newStubDB(t)

	var manager *workflowmodule.WorkflowManager

	app := fxtest.New(t,
		fx.NopLogger,
		fx.Provide(func() *zap.Logger { return zap.NewNop() }),
		fx.Provide(
			fx.Annotated{Name: "primary_db", Target: func() database.DatabaseConnector { return primary }},
			fx.Annotated{Name: "secondary_db", Target: func() database.DatabaseConnector { return secondary }},
		),
		workflowmodule.Module("workflow", workflowmodule.WithDatabaseName("secondary_db")),
		fx.Populate(fx.Annotate(&manager, fx.ParamTags(`name:"workflow"`))),
	)
	app.RequireStart()
	t.Cleanup(func() { app.RequireStop() })

	tpl := workflowmodule.WorkflowTemplate{
		ID:         "named",
		ReturnMode: workflowmodule.ReturnModeStrict,
		Stages: []workflowmodule.WorkflowStage{
			{StageIndex: 1, ReviewType: workflowmodule.ReviewTypeSingle, ApproverIDs: []string{"reviewer"}},
		},
	}
	if err := manager.SaveTemplate(ctx, tpl); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}

	// AutoMigrate should have created wf_templates on the selected DB only.
	var count int64
	if err := secondary.GetDB().Table("wf_templates").Count(&count).Error; err != nil {
		t.Fatalf("count on selected DB: %v", err)
	}
	if count != 1 {
		t.Fatalf("selected DB row count = %d, want 1", count)
	}

	// Primary should not have the table at all (no migration ran against it).
	if primary.GetDB().Migrator().HasTable("wf_templates") {
		t.Fatal("primary DB unexpectedly has wf_templates table")
	}
}

func TestSetResolver_DynamicApprovers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, mgr := startApp(t)

	// Install a custom resolver before any template is saved.
	mgr.SetResolver(workflowmodule.ApproverResolverFunc(
		func(_ workflowmodule.Application, stage workflowmodule.WorkflowStage) ([]string, error) {
			if stage.StageIndex == 1 {
				return []string{"dynamic-reviewer"}, nil
			}
			return nil, nil
		},
	))

	tpl := workflowmodule.WorkflowTemplate{
		ID:         "dynamic",
		ReturnMode: workflowmodule.ReturnModeStrict,
		Stages: []workflowmodule.WorkflowStage{
			// Empty ApproverIDs are legal once a custom resolver is installed.
			{StageIndex: 1, ReviewType: workflowmodule.ReviewTypeSingle},
		},
	}
	if err := mgr.SaveTemplate(ctx, tpl); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}

	app := workflowmodule.Application{ID: "dyn-001", WorkflowID: tpl.ID, OwnerID: "carol"}
	if err := mgr.CreateDraft(ctx, app); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, _, err := mgr.Submit(ctx, app.ID); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	final, _, err := mgr.Approve(ctx, app.ID, "dynamic-reviewer")
	if err != nil {
		t.Fatalf("Approve via dynamic resolver: %v", err)
	}
	if final.Status != workflowmodule.StatusApproved {
		t.Fatalf("final status = %q, want Approved", final.Status)
	}
}
