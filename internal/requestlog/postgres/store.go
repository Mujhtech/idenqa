// Package postgres persists the tenant-scoped operational request journal.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/requestlog"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

func (store *Store) Get(ctx context.Context, scope tenant.Scope, requestID string) (requestlog.Record, error) {
	var record requestlog.Record
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT request_id,actor_key_id,method,route_template,host(client_ip),query_parameters,body_parameters,status_code,duration_ms,occurred_at,COALESCE(trace_reference,'')
			FROM idenqa.api_request_logs WHERE tenant_id=$1 AND request_id=$2`, scope.ID().String(), requestID).
			Scan(&record.RequestID, &record.ActorKeyID, &record.Method, &record.RouteTemplate, &record.ClientIPAddress, &record.QueryParameters, &record.BodyParameters, &record.StatusCode, &record.DurationMilliseconds, &record.OccurredAt, &record.TraceReference)
		if errors.Is(err, pgx.ErrNoRows) {
			return requestlog.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query API request log: %w", err)
		}
		record.OccurredAt = record.OccurredAt.UTC()
		return nil
	})
	return record, err
}

type Store struct{ pool transactionRunner }

func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("request log postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

func (store *Store) Append(ctx context.Context, scope tenant.Scope, record requestlog.Record) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.api_request_logs
			(tenant_id,request_id,actor_key_id,method,route_template,client_ip,query_parameters,body_parameters,status_code,duration_ms,occurred_at,trace_reference)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::jsonb,$9,$10,$11,NULLIF($12,'')) ON CONFLICT (tenant_id,request_id) DO NOTHING`,
			scope.ID().String(), record.RequestID, record.ActorKeyID, record.Method, record.RouteTemplate,
			record.ClientIPAddress, string(record.QueryParameters), string(record.BodyParameters), record.StatusCode, record.DurationMilliseconds, record.OccurredAt, record.TraceReference)
		if err != nil {
			return fmt.Errorf("insert API request log: %w", err)
		}
		return nil
	})
}

func (store *Store) List(ctx context.Context, scope tenant.Scope, before requestlog.Cursor, limit int) ([]requestlog.Record, error) {
	records := make([]requestlog.Record, 0, limit)
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT request_id,actor_key_id,method,route_template,host(client_ip),query_parameters,body_parameters,status_code,duration_ms,occurred_at,COALESCE(trace_reference,'')
			FROM idenqa.api_request_logs WHERE tenant_id=$1 AND ($2::timestamptz IS NULL OR (occurred_at,request_id)<($2,$3))
			ORDER BY occurred_at DESC,request_id DESC LIMIT $4`, scope.ID().String(), nullableTime(before.OccurredAt), before.RequestID, limit)
		if err != nil {
			return fmt.Errorf("query API request logs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var record requestlog.Record
			if err := rows.Scan(&record.RequestID, &record.ActorKeyID, &record.Method, &record.RouteTemplate, &record.ClientIPAddress, &record.QueryParameters, &record.BodyParameters, &record.StatusCode, &record.DurationMilliseconds, &record.OccurredAt, &record.TraceReference); err != nil {
				return fmt.Errorf("scan API request log: %w", err)
			}
			record.OccurredAt = record.OccurredAt.UTC()
			records = append(records, record)
		}
		return rows.Err()
	})
	return records, err
}

func (store *Store) Aggregate(ctx context.Context, scope tenant.Scope, window requestlog.AnalyticsWindow) (requestlog.Analytics, error) {
	var result requestlog.Analytics
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		duration := window.To.Sub(window.From)
		if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE status_code<400),count(*) FILTER (WHERE status_code>=400),
			COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms),0)::bigint
			FROM idenqa.api_request_logs WHERE tenant_id=$1 AND occurred_at>=$2 AND occurred_at<$3`, scope.ID().String(), window.From, window.To).
			Scan(&result.Requests, &result.Successes, &result.Errors, &result.P95LatencyMilliseconds); err != nil {
			return fmt.Errorf("aggregate request-log totals: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.api_request_logs WHERE tenant_id=$1 AND occurred_at>=$2 AND occurred_at<$3`, scope.ID().String(), window.From.Add(-duration), window.From).Scan(&result.PreviousRequests); err != nil {
			return fmt.Errorf("aggregate previous request-log total: %w", err)
		}
		rows, err := tx.Query(ctx, `SELECT date_trunc('day',occurred_at) AS day,count(*),count(*) FILTER (WHERE status_code>=400)
			FROM idenqa.api_request_logs WHERE tenant_id=$1 AND occurred_at>=$2 AND occurred_at<$3 GROUP BY day ORDER BY day`, scope.ID().String(), window.From, window.To)
		if err != nil {
			return fmt.Errorf("aggregate request-log volume: %w", err)
		}
		for rows.Next() {
			var bucket requestlog.VolumeBucket
			if err := rows.Scan(&bucket.Day, &bucket.Requests, &bucket.Errors); err != nil {
				rows.Close()
				return err
			}
			bucket.Day = bucket.Day.UTC()
			result.Volume = append(result.Volume, bucket)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		rows, err = tx.Query(ctx, `SELECT method,route_template,count(*),count(*) FILTER (WHERE status_code<400),
			COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms),0)::bigint
			FROM idenqa.api_request_logs WHERE tenant_id=$1 AND occurred_at>=$2 AND occurred_at<$3
			GROUP BY method,route_template ORDER BY count(*) DESC,method,route_template LIMIT 20`, scope.ID().String(), window.From, window.To)
		if err != nil {
			return fmt.Errorf("aggregate request-log endpoints: %w", err)
		}
		for rows.Next() {
			var endpoint requestlog.EndpointMetric
			if err := rows.Scan(&endpoint.Method, &endpoint.RouteTemplate, &endpoint.Requests, &endpoint.Successes, &endpoint.P95LatencyMilliseconds); err != nil {
				rows.Close()
				return err
			}
			result.Endpoints = append(result.Endpoints, endpoint)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		rows, err = tx.Query(ctx, `SELECT (status_code/100)::text || 'xx',count(*) FROM idenqa.api_request_logs
			WHERE tenant_id=$1 AND occurred_at>=$2 AND occurred_at<$3 GROUP BY status_code/100 ORDER BY status_code/100`, scope.ID().String(), window.From, window.To)
		if err != nil {
			return fmt.Errorf("aggregate request-log statuses: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var status requestlog.StatusMetric
			if err := rows.Scan(&status.Class, &status.Count); err != nil {
				return err
			}
			result.Statuses = append(result.Statuses, status)
		}
		return rows.Err()
	})
	return result, err
}

func setScope(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return requestlog.ErrInvalid
	}
	var value string
	if err := tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value); err != nil {
		return fmt.Errorf("set request-log tenant scope: %w", err)
	}
	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
