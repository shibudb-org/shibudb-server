package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Podcopic-Labs/ShibuDb/internal/models"
	"golang.org/x/crypto/bcrypt"
)

type AuthManager struct {
	filePath string
	lock     sync.RWMutex
	users    map[string]models.User
}

func NewAuthManager(filePath string) (*AuthManager, error) {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	manager := &AuthManager{
		filePath: filePath,
		users:    make(map[string]models.User),
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err := manager.bootstrapAdmin(); err != nil {
			return nil, err
		}
	} else {
		if err := manager.load(); err != nil {
			return nil, err
		}
	}

	return manager, nil
}

// Bootstrap initial admin user
func (a *AuthManager) bootstrapAdmin() error {
	var username, password string
	reader := os.Stdin

	println("🔐 No users found. Create admin user.")
	print("Enter admin username: ")
	_, err := fmt.Fscanln(reader, &username)
	if err != nil {
		return err
	}

	print("Enter admin password: ")
	_, err = fmt.Fscanln(reader, &password)
	if err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	a.users[username] = models.User{
		Username:    username,
		Password:    string(hash),
		Role:        RoleAdmin,
		Permissions: map[string]string{},
	}

	return a.save()
}

func (a *AuthManager) load() error {
	a.lock.Lock()
	defer a.lock.Unlock()

	data, err := os.ReadFile(a.filePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &a.users)
}

func (a *AuthManager) save() error {
	data, err := json.MarshalIndent(a.users, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.filePath, data, 0644)
}

func (a *AuthManager) Authenticate(username, password string) (models.User, error) {
	user, exists := a.users[username]
	if !exists {
		return models.User{}, errors.New("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return models.User{}, errors.New("invalid password")
	}
	return user, nil
}

func (a *AuthManager) HasRole(user models.User, space string, required string) bool {
	if user.Role == RoleAdmin {
		return true
	}

	role, ok := user.Permissions[space]
	if !ok {
		return false
	}

	switch required {
	case RoleRead:
		return role == RoleRead || role == RoleWrite
	case RoleWrite:
		return role == RoleWrite
	case RoleAdmin:
		return user.Role == RoleAdmin
	}
	return false
}

func (a *AuthManager) CreateUser(username, password, role string, perms map[string]string) error {
	fmt.Println("Creating user", username, role, perms)
	a.lock.Lock()
	defer a.lock.Unlock()

	if _, exists := a.users[username]; exists {
		return errors.New("user already exists")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	a.users[username] = models.User{
		Username:    username,
		Password:    string(hash),
		Role:        role,
		Permissions: perms,
	}

	return a.save()
}

func (a *AuthManager) UpdateUserPassword(username string, password string) error {
	fmt.Println("Updating user password", username)
	a.lock.Lock()
	defer a.lock.Unlock()

	if _, exists := a.users[username]; !exists {
		return errors.New("user not found")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	user := a.users[username]

	a.users[username] = models.User{
		Username:    user.Username,
		Password:    string(hash),
		Role:        user.Role,
		Permissions: user.Permissions,
	}

	return a.save()
}

func (a *AuthManager) UpdateUserRole(username string, role string) error {
	fmt.Println("Updating user role", username, role)
	a.lock.Lock()
	defer a.lock.Unlock()

	if _, exists := a.users[username]; !exists {
		return errors.New("user not found")
	}

	user := a.users[username]

	a.users[username] = models.User{
		Username:    user.Username,
		Password:    user.Password,
		Role:        role,
		Permissions: user.Permissions,
	}

	return a.save()
}

func (a *AuthManager) UpdateUserPermissions(username string, perms map[string]string) error {
	fmt.Println("Updating user permissions", username)
	a.lock.Lock()
	defer a.lock.Unlock()

	if _, exists := a.users[username]; !exists {
		return errors.New("user not found")
	}

	user := a.users[username]

	a.users[username] = models.User{
		Username:    user.Username,
		Password:    user.Password,
		Role:        user.Role,
		Permissions: perms,
	}

	return a.save()
}

func (a *AuthManager) DeleteUser(username string) error {
	fmt.Println("Deleting user", username)
	a.lock.Lock()
	defer a.lock.Unlock()

	if _, exists := a.users[username]; !exists {
		return errors.New("user not found")
	}

	delete(a.users, username)

	return a.save()
}

func (a *AuthManager) HasUsers() bool {
	return len(a.users) > 0
}

func (a *AuthManager) GetUser(username string) (models.User, error) {
	u, ok := a.users[username]
	if !ok {
		return models.User{}, errors.New("not found")
	}
	return u, nil
}
