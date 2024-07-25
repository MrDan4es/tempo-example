package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Store struct {
	db *pgxpool.Pool
}

func New(_ context.Context, pool *pgxpool.Pool) *Store {
	return &Store{
		db: pool,
	}
}

func readRow(row pgx.Row, args ...any) error {
	err := row.Scan(args...)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return status.FromContextError(err).Err()
	case errors.Is(err, pgx.ErrNoRows):
		return status.Errorf(codes.NotFound, "not found")
	case err != nil:
		return status.Errorf(codes.Internal, fmt.Sprintf("read from database: %v", err))
	}
	return nil
}
