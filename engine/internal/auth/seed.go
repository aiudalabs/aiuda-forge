package auth

import (
	"log"
	"os"
)

// SeedAdmin creates the FIRST admin user from VIBEFORGE_ADMIN_EMAIL +
// VIBEFORGE_ADMIN_PASSWORD when the users table is empty. It is a no-op when any
// user already exists (idempotent across restarts) or when the env is not set.
//
// Logs whether a user was seeded; NEVER logs the password. Returns whether a
// user was seeded and any error.
func SeedAdmin(s *Store) (bool, error) {
	n, err := s.CountUsers()
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil // users already exist — do not re-seed
	}
	email := os.Getenv("VIBEFORGE_ADMIN_EMAIL")
	password := os.Getenv("VIBEFORGE_ADMIN_PASSWORD")
	if email == "" || password == "" {
		return false, nil // nothing to seed
	}
	u, err := s.CreateUser(email, password)
	if err != nil {
		return false, err
	}
	log.Printf("auth: seeded first admin user %s (id=%s)", u.Email, u.ID)
	return true, nil
}
