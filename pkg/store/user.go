package store

import (
	"context"
	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/postgres"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apipb "github.com/mrdan4es/tempo-example/pkg/api/v1"
)

var (
	userTable = goqu.Dialect("postgres").From("users").Prepared(true)
	userCols  = []any{
		"id",
		"username",
	}
)

func (s *Store) GetUser(ctx context.Context, username string) (*apipb.User, error) {
	selectSQL, args, err := userTable.
		Select(userCols...).
		Where(goqu.C("username").Eq(username)).
		ToSQL()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "build query: %v", err)
	}

	return readUser(s.db.QueryRow(ctx, selectSQL, args...))
}

func (s *Store) CheckUserPassword(ctx context.Context, username string, password string) error {
	selectSQL, args, err := userTable.
		Select("password").
		Where(goqu.C("username").Eq(username)).
		ToSQL()
	if err != nil {
		return status.Errorf(codes.Internal, "build query: %v", err)
	}

	var pass string
	if err := readRow(s.db.QueryRow(ctx, selectSQL, args...), &pass); err != nil {
		return status.Errorf(codes.Internal, "check password: %v", err)
	}

	if pass != password {
		return status.Errorf(codes.Unauthenticated, "invalid password")
	}

	return nil
}

func readUser(scanner pgx.Row) (*apipb.User, error) {
	var user apipb.User

	if err := readRow(
		scanner,
		&user.Id,
		&user.Username,
	); err != nil {
		return nil, err
	}

	return &user, nil
}
