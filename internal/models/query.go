package models

const (
	TypePut                   = "PUT"
	TypeGet                   = "GET"
	TypeDelete                = "DELETE"
	TypeCreateSpace           = "CREATE_SPACE"
	TypeListSpaces            = "LIST_SPACES"
	TypeDeleteSpace           = "DELETE_SPACE"
	TypeUseSpace              = "USE_SPACE"
	TypeCreateUser            = "CREATE_USER"
	TypeDeleteUser            = "DELETE_USER"
	TypeUpdateUserPassword    = "UPDATE_USER_PASSWORD"
	TypeUpdateUserRole        = "UPDATE_USER_ROLE"
	TypeUpdateUserPermissions = "UPDATE_USER_PERMISSIONS"
	TypeGetUser               = "GET_USER"
	TypeInsertVector          = "INSERT_VECTOR"
	TypeSearchTopK            = "SEARCH_TOPK"
	TypeGetVector             = "GET_VECTOR"
	TypeRangeSearch           = "RANGE_SEARCH"
)

type Query struct {
	Type       string  `json:"type"`
	Key        string  `json:"key,omitempty"`
	Value      string  `json:"value,omitempty"`
	Space      string  `json:"space,omitempty"`
	User       string  `json:"user,omitempty"`
	Data       string  `json:"data,omitempty"`
	NewUser    *User   `json:"new_user,omitempty"`
	DeleteUser *User   `json:"delete_user,omitempty"`
	EngineType string  `json:"engine_type,omitempty"`
	Dimension  int     `json:"dimension,omitempty"`
	IndexType  string  `json:"index_type,omitempty"`
	Metric     string  `json:"metric,omitempty"`
	Radius     float32 `json:"radius,omitempty"`
}
