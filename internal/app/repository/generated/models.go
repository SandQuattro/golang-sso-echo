// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.15.0

package db

import (
	"database/sql"
)

type User struct {
	ID             int64          `json:"id"`
	AuthSystem     sql.NullString `json:"auth_system"`
	Sub            sql.NullString `json:"sub"`
	Name           string         `json:"name"`
	GivenName      sql.NullString `json:"given_name"`
	FamilyName     sql.NullString `json:"family_name"`
	Avatar         sql.NullString `json:"avatar"`
	Email          string         `json:"email"`
	EmailVerified  bool           `json:"email_verified"`
	Locale         sql.NullString `json:"locale"`
	HashedPassword []byte         `json:"hashed_password"`
	Role           string         `json:"role"`
}
