package auth

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

type DB struct {
	conn *sql.DB
}

func NewDB(databaseURL string) (*DB, error) {
	conn, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

// User represents a user in the database
type User struct {
	ID                 string  `json:"id"`
	Email              string  `json:"email"`
	PasswordHash       string  `json:"-"`
	Name               *string `json:"name,omitempty"`
	Role               string  `json:"role"`
	CreatedAt          string  `json:"created_at"`
	SyncedFrom         *string `json:"synced_from,omitempty"`
	PermanentEnergy    int     `json:"permanent_energy"`
	SubscriptionEnergy int     `json:"subscription_energy"`
	DailyBonusEnergy   int     `json:"daily_bonus_energy"`
}

// Project represents a project/domain in the database
type Project struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	Domain    string  `json:"domain"`
	APIKey    string  `json:"api_key"`
	Name      *string `json:"name,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// CreateUser creates a new user
func (db *DB) CreateUser(email, passwordHash string, name *string, syncedFrom *string) (*User, error) {
	var user User
	err := db.conn.QueryRow(`
		INSERT INTO clickresearch_users (email, password_hash, name, synced_from)
		VALUES ($1, $2, $3, $4)
		RETURNING id, email, password_hash, name, role, created_at, synced_from, permanent_energy, subscription_energy, daily_bonus_energy
	`, email, passwordHash, name, syncedFrom).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Role, &user.CreatedAt, &user.SyncedFrom,
		&user.PermanentEnergy, &user.SubscriptionEnergy, &user.DailyBonusEnergy,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateUserWithID creates a user with a specific ID (for sync)
func (db *DB) CreateUserWithID(id, email, passwordHash string, name *string, syncedFrom *string) (*User, error) {
	var user User
	err := db.conn.QueryRow(`
		INSERT INTO clickresearch_users (id, email, password_hash, name, synced_from)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (email) DO UPDATE SET
			password_hash = COALESCE(EXCLUDED.password_hash, clickresearch_users.password_hash),
			name = COALESCE(EXCLUDED.name, clickresearch_users.name),
			synced_from = COALESCE(EXCLUDED.synced_from, clickresearch_users.synced_from)
		RETURNING id, email, password_hash, name, role, created_at, synced_from, permanent_energy, subscription_energy, daily_bonus_energy
	`, id, email, passwordHash, name, syncedFrom).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Role, &user.CreatedAt, &user.SyncedFrom,
		&user.PermanentEnergy, &user.SubscriptionEnergy, &user.DailyBonusEnergy,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByEmail finds a user by email
func (db *DB) GetUserByEmail(email string) (*User, error) {
	var user User
	err := db.conn.QueryRow(`
		SELECT id, email, password_hash, name, role, created_at, synced_from, permanent_energy, subscription_energy, daily_bonus_energy
		FROM clickresearch_users WHERE email = $1
	`, email).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Role, &user.CreatedAt, &user.SyncedFrom,
		&user.PermanentEnergy, &user.SubscriptionEnergy, &user.DailyBonusEnergy,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByID finds a user by ID
func (db *DB) GetUserByID(id string) (*User, error) {
	var user User
	err := db.conn.QueryRow(`
		SELECT id, email, password_hash, name, role, created_at, synced_from, permanent_energy, subscription_energy, daily_bonus_energy
		FROM clickresearch_users WHERE id = $1
	`, id).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Role, &user.CreatedAt, &user.SyncedFrom,
		&user.PermanentEnergy, &user.SubscriptionEnergy, &user.DailyBonusEnergy,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateProject creates a new project
func (db *DB) CreateProject(userID, domain string, name *string) (*Project, error) {
	apiKey := generateAPIKey()
	var project Project
	err := db.conn.QueryRow(`
		INSERT INTO clickresearch_projects (user_id, domain, api_key, name)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, domain, api_key, name, created_at
	`, userID, domain, apiKey, name).Scan(
		&project.ID, &project.UserID, &project.Domain, &project.APIKey, &project.Name, &project.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

// GetProjectsByUserID gets all projects for a user
func (db *DB) GetProjectsByUserID(userID string) ([]Project, error) {
	rows, err := db.conn.Query(`
		SELECT id, user_id, domain, api_key, name, created_at
		FROM clickresearch_projects WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.UserID, &p.Domain, &p.APIKey, &p.Name, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// GetProjectByAPIKey finds a project by API key
func (db *DB) GetProjectByAPIKey(apiKey string) (*Project, error) {
	var project Project
	err := db.conn.QueryRow(`
		SELECT id, user_id, domain, api_key, name, created_at
		FROM clickresearch_projects WHERE api_key = $1
	`, apiKey).Scan(
		&project.ID, &project.UserID, &project.Domain, &project.APIKey, &project.Name, &project.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

// DeleteProject deletes a project
func (db *DB) DeleteProject(projectID, userID string) error {
	_, err := db.conn.Exec(`DELETE FROM clickresearch_projects WHERE id = $1 AND user_id = $2`, projectID, userID)
	return err
}

// DomainExists checks if a domain exists in any project
func (db *DB) DomainExists(domain string) bool {
	var exists bool
	err := db.conn.QueryRow(`SELECT EXISTS(SELECT 1 FROM clickresearch_projects WHERE domain = $1)`, domain).Scan(&exists)
	return err == nil && exists
}

// GetAllDomains returns all registered domains
func (db *DB) GetAllDomains() ([]string, error) {
	rows, err := db.conn.Query(`SELECT domain FROM clickresearch_projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}
	return domains, rows.Err()
}

// UpdateEnergy updates energy levels for a user
func (db *DB) UpdateEnergy(userID string, permanent, subscription, dailyBonus int) error {
	_, err := db.conn.Exec(`
		UPDATE clickresearch_users
		SET permanent_energy = $2, subscription_energy = $3, daily_bonus_energy = $4
		WHERE id = $1
	`, userID, permanent, subscription, dailyBonus)
	return err
}

// UpdateEnergyByEmail updates energy levels for a user by email
func (db *DB) UpdateEnergyByEmail(email string, permanent, subscription, dailyBonus int) error {
	_, err := db.conn.Exec(`
		UPDATE clickresearch_users
		SET permanent_energy = $2, subscription_energy = $3, daily_bonus_energy = $4
		WHERE email = $1
	`, email, permanent, subscription, dailyBonus)
	return err
}

// ProjectWithUser includes user info for admin view
type ProjectWithUser struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	UserEmail string  `json:"user_email"`
	Domain    string  `json:"domain"`
	APIKey    string  `json:"api_key"`
	Name      *string `json:"name,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// GetAllProjectsAdmin returns all projects with user info (admin only)
func (db *DB) GetAllProjectsAdmin() ([]ProjectWithUser, error) {
	rows, err := db.conn.Query(`
		SELECT p.id, p.user_id, u.email, p.domain, p.api_key, p.name, p.created_at
		FROM clickresearch_projects p
		JOIN clickresearch_users u ON p.user_id = u.id
		ORDER BY p.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []ProjectWithUser
	for rows.Next() {
		var p ProjectWithUser
		if err := rows.Scan(&p.ID, &p.UserID, &p.UserEmail, &p.Domain, &p.APIKey, &p.Name, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// GetAllUsersAdmin returns all users (admin only)
func (db *DB) GetAllUsersAdmin() ([]User, error) {
	rows, err := db.conn.Query(`
		SELECT id, email, password_hash, name, role, created_at, synced_from, permanent_energy, subscription_energy, daily_bonus_energy
		FROM clickresearch_users
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.Role, &u.CreatedAt, &u.SyncedFrom,
			&u.PermanentEnergy, &u.SubscriptionEnergy, &u.DailyBonusEnergy); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}
