package main

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"

	rqdb "github.com/rqlite/rqlite/db"
	rqstore "github.com/rqlite/rqlite/store"
)

type RqliteResult struct {
	result *rqdb.Result
}

func (r *RqliteResult) LastInsertId() (int64, error) {
	if r.result.Error != "" {
		return -1, fmt.Errorf(r.result.Error)
	}

	return r.result.LastInsertID, nil
}

func (r *RqliteResult) RowsAffected() (int64, error) {
	if r.result.Error != "" {
		return -1, fmt.Errorf(r.result.Error)
	}

	return r.result.RowsAffected, nil
}

type RqliteRows struct {
	rows *rqdb.Rows
	cur  int
}

func (r *RqliteRows) Columns() []string {
	return r.rows.Columns
}

func (r *RqliteRows) Close() error {
	/* no-op */
	return nil
}

func (r *RqliteRows) Next(dest []driver.Value) error {
	if r.cur >= len(r.rows.Values) {
		return io.EOF
	}

	row := r.rows.Values[r.cur]
	r.cur++

	for i := range dest {
		dest[i] = row[i]
	}

	return nil
}

type RqliteStmt struct {
	q    string
	conn *RqliteConn
}

func (s *RqliteStmt) Close() error {
	/* no-op */
	return nil
}

func (s *RqliteStmt) NumInput() int {
	return strings.Count(s.q, "?")
}

func (s *RqliteStmt) Exec(args []driver.Value) (driver.Result, error) {
	argsIf := []interface{}{}
	for _, v := range args {
		argsIf = append(argsIf, v.(interface{}))
	}

	q := fmt.Sprintf(strings.Replace(s.q, "?", "%v", -1), argsIf)

	results, err := store.Execute([]string{q}, false, true)
	if isNotLeaderErr(err) {
		leader, err := peerStore.Leader()
		if err != nil {
			return nil, err
		}

		l, err := connectTo(leader.Addr, leader.Certificate)
		if err != nil {
			return nil, err
		}

		result, err := l.ClusterDBExecute(q)
		if err != nil {
			return nil, err
		}

		return &RqliteResult{result}, nil
	}

	if len(results) != 1 {
		return nil, fmt.Errorf("wrong number of results, got %d", len(results))
	}

	if results[0].Error != "" {
		return nil, fmt.Errorf(results[0].Error)
	}

	return &RqliteResult{results[0]}, err
}

func (s *RqliteStmt) Query(args []driver.Value) (driver.Rows, error) {
	argsIf := []interface{}{}
	for _, v := range args {
		argsIf = append(argsIf, v.(interface{}))
	}

	q := fmt.Sprintf(strings.Replace(s.q, "?", "%v", -1), argsIf)

	result, err := store.Query([]string{q}, false, true, rqstore.Weak)
	if isNotLeaderErr(err) {
		leader, err := peerStore.Leader()
		if err != nil {
			return nil, err
		}

		l, err := connectTo(leader.Addr, leader.Certificate)
		if err != nil {
			return nil, err
		}

		result, err := l.ClusterDBQuery(q)
		if err != nil {
			return nil, err
		}

		return &RqliteRows{rows: result}, nil

	} else if err != nil {
		return nil, err
	}

	if len(result) != 1 {
		return nil, fmt.Errorf("wrong number of rows, expected %d", len(result))
	}

	return &RqliteRows{rows: result[0]}, nil
}

type RqliteConn struct {
	closed    bool
}

func (c *RqliteConn) Prepare(query string) (driver.Stmt, error) {
	if c.closed {
		return nil, fmt.Errorf("connection is closed")
	}
	return &RqliteStmt{query}, nil
}

func (c *RqliteConn) Close() error {
	if c.closed {
		return nil, fmt.Errorf("connection is closed")
	}

	c.closed = true
	return nil
}

func (c *RqliteConn) Begin() (driver.Tx, error) {
	return store.Begin()
}

type RqliteDriver struct {}

func (d RqliteDriver) Open(name string) (driver.Conn, error) {
	return RqliteConn{}, nil
}

// XXX: move this somewhere else
func copyToCluster(db *sql.DB) error {
	/*
	certs, err := dbCertsGet(db)
	if err != nil {
		return err
	}
	*/

	// XXX: fixme
	return nil
}
