package checks

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/uptimy/agent/internal/monitor"
)

func init() {
	register(monitor.TypePostgres, func(ctx context.Context, _ *Checker, m monitor.Check) Outcome { return postgres(ctx, m) })
	register(monitor.TypeMySQL, func(ctx context.Context, _ *Checker, m monitor.Check) Outcome { return mysqlCheck(ctx, m) })
}

// Database checks open a fresh connection every time, log in, run one query
// and disconnect. A pool would hide exactly what they're for: failed logins,
// a full connection limit, a server that accepts TCP but can't serve.
// Queries run in a read-only transaction so a monitor can't change data.

const defaultQuery = "SELECT 1"

// appName labels the agent's sessions in pg_stat_activity and the MySQL
// processlist, so DBAs can tell what the connections are.
const appName = "uptimy-agent"

func postgres(ctx context.Context, m monitor.Check) Outcome {
	target, err := m.ResolvedTarget()
	if err != nil {
		return Outcome{Message: err.Error()}
	}
	cfg, err := pgx.ParseConfig(target)
	if err != nil {
		return dbFailure(target, err)
	}
	if cfg.RuntimeParams["application_name"] == "" {
		cfg.RuntimeParams["application_name"] = appName
	}
	// Unnamed statements over the extended protocol: work behind PgBouncer
	// in transaction mode (no prepared statement to keep), and reject
	// multi-statement strings, so "SELECT 1; COMMIT; ..." can't leave the
	// read-only transaction.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeExec

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return dbFailure(target, err)
	}
	defer closeQuietly(func(ctx context.Context) error { return conn.Close(ctx) })

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return dbFailure(target, err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck // read-only; the connection closes anyway

	rows, err := tx.Query(ctx, queryOf(m))
	if err != nil {
		return dbFailure(target, err)
	}
	var first *string
	if rows.Next() {
		values, err := rows.Values()
		if err != nil {
			rows.Close()
			return dbFailure(target, err)
		}
		if len(values) > 0 {
			s := formatValue(values[0])
			first = &s
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return dbFailure(target, err)
	}
	server := "PostgreSQL"
	if v := conn.PgConn().ParameterStatus("server_version"); v != "" {
		server += " " + strings.Fields(v)[0]
	}
	return queryOutcome(m, first, server)
}

func mysqlCheck(ctx context.Context, m monitor.Check) Outcome {
	target, err := m.ResolvedTarget()
	if err != nil {
		return Outcome{Message: err.Error()}
	}
	cfg, err := mysqlConfig(target)
	if err != nil {
		return dbFailure(target, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		cfg.Timeout = time.Until(deadline)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return dbFailure(target, err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	conn, err := db.Conn(ctx)
	if err != nil {
		return dbFailure(target, err)
	}
	defer conn.Close()

	// Session-wide rather than a READ ONLY transaction: DDL commits the
	// transaction implicitly and would then run, but the session mode blocks
	// it. The driver sends one statement per query unless multiStatements is
	// set, which it isn't.
	if _, err := conn.ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY"); err != nil {
		return dbFailure(target, err)
	}
	rows, err := conn.QueryContext(ctx, queryOf(m))
	if err != nil {
		return dbFailure(target, err)
	}
	defer rows.Close()
	var first *string
	if rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return dbFailure(target, err)
		}
		dest := make([]any, len(cols))
		for i := range dest {
			dest[i] = new(sql.RawBytes)
		}
		if err := rows.Scan(dest...); err != nil {
			return dbFailure(target, err)
		}
		if len(dest) > 0 {
			s := "NULL"
			if b := *dest[0].(*sql.RawBytes); b != nil {
				s = string(b)
			}
			first = &s
		}
	}
	if err := rows.Err(); err != nil {
		return dbFailure(target, err)
	}
	return queryOutcome(m, first, "MySQL")
}

// mysqlConfig turns mysql://user:pass@host:port/db?tls=... into the driver's
// config. tls is true, false, skip-verify or preferred (the default: TLS when
// the server offers it, like Postgres' sslmode=prefer).
func mysqlConfig(target string) (*mysql.Config, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	if u.Port() == "" {
		cfg.Addr = net.JoinHostPort(u.Hostname(), "3306")
	}
	cfg.User = u.User.Username()
	cfg.Passwd, _ = u.User.Password()
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	cfg.ConnectionAttributes = "program_name:" + appName
	tlsMode := u.Query().Get("tls")
	if tlsMode == "" {
		tlsMode = "preferred"
	}
	switch tlsMode {
	case "true", "false", "skip-verify", "preferred":
		cfg.TLSConfig = tlsMode
	default:
		return nil, fmt.Errorf("unsupported tls=%q (use true, false, skip-verify or preferred)", tlsMode)
	}
	return cfg, nil
}

func queryOf(m monitor.Check) string {
	if m.Config.Query != "" {
		return m.Config.Query
	}
	return defaultQuery
}

func queryOutcome(m monitor.Check, first *string, server string) Outcome {
	if m.Config.Expected == "" {
		return Outcome{OK: true, Message: "query ok (" + server + ")"}
	}
	if first == nil {
		return Outcome{Message: fmt.Sprintf("expected %q, query returned no rows", m.Config.Expected), ErrorClass: classAssertion}
	}
	if strings.TrimSpace(*first) != m.Config.Expected {
		return Outcome{Message: fmt.Sprintf("expected %q, got %q", m.Config.Expected, truncate(*first, 100)), ErrorClass: classAssertion}
	}
	return Outcome{OK: true, Message: fmt.Sprintf("returned %q (%s)", m.Config.Expected, server)}
}

func formatValue(v any) string {
	switch v := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	default:
		return fmt.Sprint(v)
	}
}

// dbFailure reports err with the connection's password scrubbed, in case a
// driver echoes the connection string.
func dbFailure(target string, err error) Outcome {
	msg := trimErr(err)
	if u, perr := url.Parse(target); perr == nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "••••••")
		}
	}
	return Outcome{Message: msg, ErrorClass: classify(err)}
}

func closeQuietly(close func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = close(ctx)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
