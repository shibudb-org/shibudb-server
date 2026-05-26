package queryengine

import (
	"strings"
	"testing"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/auth"
	"github.com/shibudb.org/shibudb-server/internal/models"
	"github.com/shibudb.org/shibudb-server/internal/spaces"
)

// mockAuth returns admin for any user so space create and vector ops can run in tests.
type mockAuth struct{}

func (m *mockAuth) GetUser(username string) (models.User, error) {
	return models.User{Username: username, Role: auth.RoleAdmin}, nil
}

func (m *mockAuth) CreateUser(username, password, role string, perms map[string]string) error {
	return nil
}

func (m *mockAuth) UpdateUserPassword(username, password string) error {
	return nil
}

func (m *mockAuth) UpdateUserRole(username, role string) error {
	return nil
}

func (m *mockAuth) UpdateUserPermissions(username string, perms map[string]string) error {
	return nil
}

func (m *mockAuth) DeleteUser(username string) error {
	return nil
}

func (m *mockAuth) ListUsers() []models.User {
	return []models.User{}
}

func TestQueryEngine_DeleteVector(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()

	qe := NewQueryEngine(sm, &mockAuth{})

	space := "vec_delete_test"
	// Create vector space
	_, err := qe.Execute(models.Query{
		Type:       models.TypeCreateSpace,
		Space:      space,
		EngineType: "vector",
		Dimension:  4,
		IndexType:  "Flat",
		Metric:     "L2",
		User:       "admin",
		EnableWAL:  true,
	})
	if err != nil {
		t.Fatalf("CreateSpace failed: %v", err)
	}

	// Insert vector id=1
	_, err = qe.Execute(models.Query{
		Type:  models.TypeInsertVector,
		Space: space,
		Key:   "1",
		Value: "0.1,0.2,0.3,0.4",
	})
	if err != nil {
		t.Fatalf("INSERT_VECTOR failed: %v", err)
	}
	time.Sleep(500 * time.Millisecond) // allow batched persistence to flush

	// Get vector (should succeed)
	res, err := qe.Execute(models.Query{
		Type:  models.TypeGetVector,
		Space: space,
		Key:   "1",
	})
	if err != nil {
		t.Fatalf("GET_VECTOR before delete failed: %v", err)
	}
	if res == "" || !strings.Contains(res, "0.1") {
		t.Errorf("Expected vector data, got %q", res)
	}

	// Delete vector
	res, err = qe.Execute(models.Query{
		Type:  models.TypeDeleteVector,
		Space: space,
		Key:   "1",
	})
	if err != nil {
		t.Fatalf("DELETE_VECTOR failed: %v", err)
	}
	if res != "VECTOR_DELETED" {
		t.Errorf("Expected VECTOR_DELETED, got %q", res)
	}

	// Get vector again (should fail)
	res, err = qe.Execute(models.Query{
		Type:  models.TypeGetVector,
		Space: space,
		Key:   "1",
	})
	if err == nil {
		t.Errorf("Expected error from GET_VECTOR after delete, got res=%q", res)
	}
	if err != nil && !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 'not found' error, got: %v", err)
	}
}

func TestQueryEngine_DeleteVector_InvalidID(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()

	qe := NewQueryEngine(sm, &mockAuth{})

	space := "vec_delete_invalid"
	_, _ = qe.Execute(models.Query{
		Type:       models.TypeCreateSpace,
		Space:      space,
		EngineType: "vector",
		Dimension:  4,
		IndexType:  "Flat",
		Metric:     "L2",
		User:       "admin",
		EnableWAL:  true,
	})

	// Delete with non-numeric key should fail
	_, err := qe.Execute(models.Query{
		Type:  models.TypeDeleteVector,
		Space: space,
		Key:   "not-a-number",
	})
	if err == nil {
		t.Error("Expected error for invalid vector id")
	}
}

func TestQueryEngine_DeleteVector_NoSpace(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()

	qe := NewQueryEngine(sm, &mockAuth{})

	_, err := qe.Execute(models.Query{
		Type:  models.TypeDeleteVector,
		Space: "",
		Key:   "1",
	})
	if err == nil {
		t.Error("Expected error when no space selected")
	}
}

func TestQueryEngine_CreateSpaceSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	qe := NewQueryEngine(sm, &mockAuth{})

	if _, err := qe.Execute(models.Query{
		Type:      models.TypeCreateSpace,
		Space:     "restart_space",
		User:      "admin",
		EnableWAL: true,
	}); err != nil {
		t.Fatalf("CreateSpace failed: %v", err)
	}
	sm.CloseAll()

	reloaded := spaces.NewSpaceManager(dir)
	defer reloaded.CloseAll()
	qe = NewQueryEngine(reloaded, &mockAuth{})

	res, err := qe.Execute(models.Query{Type: models.TypeListSpaces})
	if err != nil {
		t.Fatalf("TypeListSpaces failed after restart: %v", err)
	}
	if !strings.Contains(res, `"restart_space"`) {
		t.Fatalf("ListSpaces response %q does not include restart_space", res)
	}
}

func TestQueryEngine_UpdateSpaceSettings(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()

	qe := NewQueryEngine(sm, &mockAuth{})

	if _, err := qe.Execute(models.Query{
		Type:       models.TypeCreateSpace,
		Space:      "settings_space",
		EngineType: "key-value",
		User:       "admin",
	}); err != nil {
		t.Fatalf("CreateSpace failed: %v", err)
	}

	res, err := qe.Execute(models.Query{
		Type:                   models.TypeUpdateSpaceSettings,
		Space:                  "settings_space",
		User:                   "admin",
		SegmentRolloverBytes:   2048,
		MaxSegmentsBeforeMerge: 9,
	})
	if err != nil {
		t.Fatalf("UpdateSpaceSettings failed: %v", err)
	}
	if res != "SPACE_SETTINGS_UPDATED" {
		t.Fatalf("result = %q, want SPACE_SETTINGS_UPDATED", res)
	}

	meta, ok := sm.SpaceMeta("settings_space")
	if !ok {
		t.Fatal("SpaceMeta missing after update")
	}
	if meta.SegmentRolloverBytes != 2048 {
		t.Fatalf("SegmentRolloverBytes = %d, want 2048", meta.SegmentRolloverBytes)
	}
	if meta.MaxSegmentsBeforeMerge != 9 {
		t.Fatalf("MaxSegmentsBeforeMerge = %d, want 9", meta.MaxSegmentsBeforeMerge)
	}
}
