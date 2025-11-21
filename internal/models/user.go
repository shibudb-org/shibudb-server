package models

type User struct {
	Username    string            `json:"username"`
	Password    string            `json:"password"`    // hashed password
	Role        string            `json:"role"`        // "admin" | "read" | "write"
	Permissions map[string]string `json:"permissions"` // table -> role
}
