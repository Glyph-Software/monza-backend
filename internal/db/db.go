package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func New(ctx context.Context, connString string) (*DB, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, err
	}

	return &DB{Pool: pool}, nil
}

func (d *DB) Close() {
	if d == nil || d.Pool == nil {
		return
	}

	d.Pool.Close()
}

type Batch = pgx.Batch

func (d *DB) SendBatch(ctx context.Context, b *Batch) error {
	br := d.Pool.SendBatch(ctx, b)
	defer br.Close()
	_, err := br.Exec()
	return err
}


