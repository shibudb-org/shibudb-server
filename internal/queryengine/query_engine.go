package queryengine

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Podcopic-Labs/ShibuDb/internal/auth"
	"github.com/Podcopic-Labs/ShibuDb/internal/models"
	"github.com/Podcopic-Labs/ShibuDb/internal/spaces"
	"github.com/Podcopic-Labs/ShibuDb/internal/storage"
)

// Add this interface above QueryEngine
type AuthManagerIface interface {
	GetUser(username string) (models.User, error)
	CreateUser(username, password, role string, perms map[string]string) error
	UpdateUserPassword(username string, password string) error
	UpdateUserRole(username string, role string) error
	UpdateUserPermissions(username string, perms map[string]string) error
	DeleteUser(username string) error
}

type QueryEngine struct {
	spaceManager *spaces.SpaceManager
	authManager  AuthManagerIface
}

func NewQueryEngine(spaceManager *spaces.SpaceManager, authManager AuthManagerIface) *QueryEngine {
	return &QueryEngine{
		spaceManager: spaceManager,
		authManager:  authManager,
	}
}

func getUserResponse(u *models.User) string {
	if u == nil {
		return "User: <nil>"
	}

	result := fmt.Sprintf("Username: %s | Role: %s", u.Username, u.Role)
	if len(u.Permissions) == 0 {
		result += " | Permissions: None"
	} else {
		result += " | Permissions: "
		pairs := []string{}
		for table, role := range u.Permissions {
			pairs = append(pairs, fmt.Sprintf("%s=%s", table, role))
		}
		result += strings.Join(pairs, ", ")
	}
	return result
}

func (qe *QueryEngine) Execute(query models.Query) (string, error) {
	log.Println("Query:", query.Type)

	switch query.Type {
	case models.TypeGetUser:
		if query.User == "" {
			return "", errors.New("unauthenticated")
		}
		admin, err := qe.authManager.GetUser(query.User)
		if err != nil || admin.Role != auth.RoleAdmin {
			return "", errors.New("only admin can get user info")
		}
		if query.Data == "" {
			return "", errors.New("query data missing")
		}
		user, err := qe.authManager.GetUser(query.Data)
		if err != nil {
			return "", err
		}
		return getUserResponse(&user), nil
	case models.TypeCreateUser:
		log.Println("Creating user:", query)
		if query.User == "" {
			return "", errors.New("unauthenticated")
		}
		admin, err := qe.authManager.GetUser(query.User)
		if err != nil || admin.Role != auth.RoleAdmin {
			return "", errors.New("only admin can create users")
		}
		if query.NewUser == nil {
			return "", errors.New("new user data missing")
		}
		err = qe.authManager.CreateUser(query.NewUser.Username, query.NewUser.Password, query.NewUser.Role, query.NewUser.Permissions)
		if err != nil {
			return "", err
		}
		return "USER_CREATED", nil
	case models.TypeUpdateUserPassword:
		log.Println("Updating user password:", query.NewUser)
		if query.User == "" {
			return "", errors.New("unauthenticated")
		}
		admin, err := qe.authManager.GetUser(query.User)
		if err != nil || admin.Role != auth.RoleAdmin {
			return "", errors.New("only admin can update users")
		}
		if query.NewUser == nil {
			return "", errors.New("user data missing")
		}
		err = qe.authManager.UpdateUserPassword(query.NewUser.Username, query.NewUser.Password)
		if err != nil {
			return "", err
		}
		return "USER_PASSWORD_UPDATED", nil
	case models.TypeUpdateUserRole:
		log.Println("Updating user role:", query.NewUser)
		if query.User == "" {
			return "", errors.New("unauthenticated")
		}
		admin, err := qe.authManager.GetUser(query.User)
		if err != nil || admin.Role != auth.RoleAdmin {
			return "", errors.New("only admin can update users")
		}
		if query.NewUser == nil {
			return "", errors.New("user data missing")
		}
		err = qe.authManager.UpdateUserRole(query.NewUser.Username, query.NewUser.Role)
		if err != nil {
			return "", err
		}
		return "USER_ROLE_UPDATED", nil
	case models.TypeUpdateUserPermissions:
		log.Println("Updating user permissions:", query.NewUser)
		if query.User == "" {
			return "", errors.New("unauthenticated")
		}
		admin, err := qe.authManager.GetUser(query.User)
		if err != nil || admin.Role != auth.RoleAdmin {
			return "", errors.New("only admin can update users")
		}
		if query.NewUser == nil {
			return "", errors.New("user data missing")
		}
		err = qe.authManager.UpdateUserPermissions(query.NewUser.Username, query.NewUser.Permissions)
		if err != nil {
			return "", err
		}
		return "USER_ROLE_UPDATED", nil
	case models.TypeDeleteUser:
		log.Println("Deleting user:", query.DeleteUser)
		if query.User == "" {
			return "", errors.New("unauthenticated")
		}
		admin, err := qe.authManager.GetUser(query.User)
		if err != nil || admin.Role != auth.RoleAdmin {
			return "", errors.New("only admin can delete users")
		}
		if query.DeleteUser == nil {
			return "", errors.New("user data missing")
		}
		err = qe.authManager.DeleteUser(query.DeleteUser.Username)
		if err != nil {
			return "", err
		}
		return "USER_DELETED", nil
	case models.TypeUseSpace:
		if query.Space == "" {
			return "", errors.New("space name required")
		}
		_, err := qe.spaceManager.UseSpace(query.Space)
		if err != nil {
			return "", err
		}
		return "SPACE_CHANGED", nil

	case models.TypeCreateSpace:
		if query.Space == "" {
			return "", errors.New("space name required")
		}
		admin, err := qe.authManager.GetUser(query.User)
		if err != nil || admin.Role != auth.RoleAdmin {
			return "", errors.New("only admin can create spaces")
		}
		if query.EngineType == "" {
			query.EngineType = "key-value"
		}

		// Set defaults for vector spaces if not specified
		indexType := query.IndexType
		metric := query.Metric
		if query.EngineType == "vector" {
			if indexType == "" {
				indexType = "Flat"
			}
		}

		_, err = qe.spaceManager.CreateSpaceWithWAL(query.Space, query.EngineType, query.Dimension, indexType, metric, query.EnableWAL)
		if err != nil {
			return "", err
		}
		return "SPACE_CREATED", nil

	case models.TypeDeleteSpace:
		if query.Data == "" {
			return "", errors.New("space name required")
		}

		admin, err := qe.authManager.GetUser(query.User)
		if err != nil || admin.Role != auth.RoleAdmin {
			return "", errors.New("only admin can delete spaces")
		}

		err = qe.spaceManager.DeleteSpace(query.Data)
		if err != nil {
			return "", err
		}
		return "SPACE_DELETED", nil

	case models.TypeListSpaces:
		spaces := qe.spaceManager.ListSpaces()
		return serializeSpaces(spaces), nil

	case models.TypePut, models.TypeGet, models.TypeDelete:
		if query.Space == "" {
			return "", errors.New("no table selected")
		}
		eng, ok := qe.spaceManager.GetSpace(query.Space)
		if !ok {
			return "", errors.New("table space does not exist")
		}
		// Determine engine type
		meta, metaOk := qe.spaceManager.SpaceMeta(query.Space)
		if !metaOk {
			return "", errors.New("space metadata not found")
		}
		if meta.EngineType != "key-value" {
			return "", errors.New("operation not supported: not a key-value space")
		}
		engine, ok := eng.(storage.KeyValueEngine)
		if !ok {
			return "", errors.New("internal error: engine is not KeyValueEngine")
		}
		switch query.Type {
		case models.TypePut:
			return "OK", engine.Put(query.Key, query.Value)
		case models.TypeGet:
			return engine.Get(query.Key)
		case models.TypeDelete:
			err := engine.Delete(query.Key)
			if err != nil {
				return "", err
			}
			return "DELETED", nil
		}
	// Vector operations (example, add more as needed)
	case "INSERT_VECTOR":
		if query.Space == "" {
			return "", errors.New("no space selected")
		}
		eng, ok := qe.spaceManager.GetSpace(query.Space)
		if !ok {
			return "", errors.New("space does not exist")
		}
		meta, metaOk := qe.spaceManager.SpaceMeta(query.Space)
		if !metaOk || meta.EngineType != "vector" {
			return "", errors.New("operation not supported: not a vector space")
		}
		engine, ok := eng.(storage.VectorEngine)
		if !ok {
			return "", errors.New("internal error: engine is not VectorEngine")
		}
		// Expect query.Key as string id, query.Value as comma-separated floats
		var id int64
		_, err := fmt.Sscanf(query.Key, "%d", &id)
		if err != nil {
			return "", errors.New("invalid vector id")
		}
		vector, err := parseVector(query.Value, meta.Dimension)
		if err != nil {
			return "", err
		}
		err = engine.InsertVector(id, vector)
		if err != nil {
			return "", err
		}
		return "VECTOR_INSERTED", nil
	case "SEARCH_TOPK":
		if query.Space == "" {
			return "", errors.New("no space selected")
		}
		eng, ok := qe.spaceManager.GetSpace(query.Space)
		if !ok {
			return "", errors.New("space does not exist")
		}
		meta, metaOk := qe.spaceManager.SpaceMeta(query.Space)
		if !metaOk || meta.EngineType != "vector" {
			return "", errors.New("operation not supported: not a vector space")
		}
		engine, ok := eng.(storage.VectorEngine)
		if !ok {
			return "", errors.New("internal error: engine is not VectorEngine")
		}
		vector, err := parseVector(query.Value, meta.Dimension)
		if err != nil {
			return "", err
		}
		k := query.Dimension
		if k <= 0 {
			k = 1
		}
		ids, dists, err := engine.SearchTopK(vector, k)
		if err != nil {
			return "", err
		}
		return formatSearchResults(ids, dists), nil
	case "RANGE_SEARCH":
		if query.Space == "" {
			return "", errors.New("no space selected")
		}
		eng, ok := qe.spaceManager.GetSpace(query.Space)
		if !ok {
			return "", errors.New("space does not exist")
		}
		meta, metaOk := qe.spaceManager.SpaceMeta(query.Space)
		if !metaOk || meta.EngineType != "vector" {
			return "", errors.New("operation not supported: not a vector space")
		}
		engine, ok := eng.(storage.VectorEngine)
		if !ok {
			return "", errors.New("internal error: engine is not VectorEngine")
		}
		vector, err := parseVector(query.Value, meta.Dimension)
		if err != nil {
			return "", err
		}
		radius := query.Radius
		if radius <= 0 {
			radius = 1.0 // default radius if not set
		}
		ids, dists, err := engine.RangeSearch(vector, radius)
		if err != nil {
			return "", err
		}
		return formatSearchResults(ids, dists), nil
	case "GET_VECTOR":
		if query.Space == "" {
			return "", errors.New("no space selected")
		}
		eng, ok := qe.spaceManager.GetSpace(query.Space)
		if !ok {
			return "", errors.New("space does not exist")
		}
		meta, metaOk := qe.spaceManager.SpaceMeta(query.Space)
		if !metaOk || meta.EngineType != "vector" {
			return "", errors.New("operation not supported: not a vector space")
		}
		engine, ok := eng.(storage.VectorEngine)
		if !ok {
			return "", errors.New("internal error: engine is not VectorEngine")
		}
		var id int64
		_, err := fmt.Sscanf(query.Key, "%d", &id)
		if err != nil {
			return "", errors.New("invalid vector id")
		}
		vec, err := engine.GetVectorByID(id)
		if err != nil {
			return "", err
		}
		return formatVector(vec), nil
	}

	return "", errors.New("unsupported query type")
}

func serializeSpaces(spaces []string) string {
	json := `{"status":"OK","spaces":[`
	for i, name := range spaces {
		if i > 0 {
			json += ","
		}
		json += `"` + name + `"`
	}
	json += "]}"
	return json
}

// Helper to parse vector from string
func parseVector(s string, dim int) ([]float32, error) {
	parts := strings.Split(s, ",")
	if len(parts) != dim {
		return nil, fmt.Errorf("vector dimension mismatch: expected %d, got %d", dim, len(parts))
	}
	vec := make([]float32, dim)
	for i, p := range parts {
		var f float32
		_, err := fmt.Sscanf(strings.TrimSpace(p), "%f", &f)
		if err != nil {
			return nil, fmt.Errorf("invalid float at position %d: %v", i, err)
		}
		vec[i] = f
	}
	return vec, nil
}

// Helper to format vector
func formatVector(vec []float32) string {
	parts := make([]string, len(vec))
	for i, v := range vec {
		parts[i] = fmt.Sprintf("%f", v)
	}
	return strings.Join(parts, ",")
}

func formatSearchResults(ids []int64, dists []float32) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i := range ids {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("{\"id\": %d, \"distance\": %f}", ids[i], dists[i]))
	}
	sb.WriteString("]")
	return sb.String()
}
