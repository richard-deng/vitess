// Copyright 2015, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sqlannotation provides functions
// for annotating DML statements with keyspace-id
// comments and parsing them. These annotations
// are used during filtered-replication to route
// the DML statement to the correct shard.
// TOOD(erez): Move the code for the "_stream" annotations
// from vttablet to here.
package sqlannotation

import (
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/youtube/vitess/go/stats"
	"github.com/youtube/vitess/go/vt/logutil"
)

const (
	filteredReplicationUnfriendlyAnnotation = "/* vtgate:: filtered_replication_unfriendly */"
)

var (
	filteredReplicationUnfriendlyStatementsCount = stats.NewInt("FilteredReplicationUnfriendlyStatementsCount")
	filteredReplicationUnfriendlyStatementLogger = logutil.NewThrottledLogger("FilteredReplicationUnfriendlyStatement", 5*time.Second)
)

// AnnotateIfDML annotates 'sql' based on 'keyspaceIDs'
//
// If 'sql' is not a DML statement no annotation is added.
// If 'sql' is a DML statement and contains exactly one keyspaceID
//    it is used to annotate 'sql'
// Otherwise 'sql' is annotated as replication-unfriendly.
func AnnotateIfDML(sql string, keyspaceIDs [][]byte) string {
	if !IsDML(sql) {
		return sql
	}
	if len(keyspaceIDs) == 1 {
		return AddKeyspaceID(sql, keyspaceIDs[0], "")
	}
	filteredReplicationUnfriendlyStatementsCount.Add(1)
	filteredReplicationUnfriendlyStatementLogger.Warningf("filtered-replication-unfriendly SQL statement detected: %q", sql)
	return sql + filteredReplicationUnfriendlyAnnotation
}

// AddKeyspaceID returns a copy of 'sql' annotated
// with the given keyspace id. It also appends the
// additional trailingComments, if any.
func AddKeyspaceID(sql string, keyspaceID []byte, trailingComments string) string {
	return fmt.Sprintf("%s /* vtgate:: keyspace_id:%s */%s",
		sql, hex.EncodeToString(keyspaceID), trailingComments)
}

// IsDML returns true if 'querySQL' is an INSERT, UPDATE or DELETE statement.
func IsDML(sql string) bool {
	sql = strings.TrimLeftFunc(sql, unicode.IsSpace)
	end := strings.IndexFunc(sql, unicode.IsSpace)
	if end == -1 {
		return false
	}
	word := sql[:end]
	return strings.EqualFold(word, "insert") ||
		strings.EqualFold(word, "update") ||
		strings.EqualFold(word, "delete")
}

// ExtractKeySpaceID parses the annotation of the given statement and tries
// to extract the keyspace id.
// If a keyspace-id comment exists 'keyspaceID' is set to the parsed keyspace id
// and err is set to nil; otherwise, if a filtered-replication-unfriendly comment exists
// or some other parsing error occured, keyspaceID is set to nil and err is set to a non-nil
// error value.
func ExtractKeySpaceID(sql string) (keyspaceID []byte, err error) {
	keyspaceIDString, hasKeySpaceID := extractStringBetween(sql, "/* vtgate:: keyspace_id:", " ")
	hasUnfriendlyAnnotation := (strings.Index(sql, filteredReplicationUnfriendlyAnnotation) != -1)
	err = nil
	if hasKeySpaceID {
		if hasUnfriendlyAnnotation {
			keyspaceID = nil
			err = &ExtractKeySpaceIDError{
				Kind:    ExtractKeySpaceIDParseError,
				Message: fmt.Sprintf("Conflicting annotations in statement '%v'", sql),
			}
			return
		}
		keyspaceID, err = hex.DecodeString(keyspaceIDString)
		if err != nil {
			keyspaceID = nil
			err = &ExtractKeySpaceIDError{
				Kind: ExtractKeySpaceIDParseError,
				Message: fmt.Sprintf(
					"Error parsing keyspace id value in statement: %v (%v)", sql, err),
			}
		}
		return
	}

	if hasUnfriendlyAnnotation {
		err = &ExtractKeySpaceIDError{
			Kind:    ExtractKeySpaceIDReplicationUnfriendlyError,
			Message: fmt.Sprintf("Statement: %v", sql),
		}
		keyspaceID = nil
		return
	}

	// No annotations.
	keyspaceID = nil
	err = &ExtractKeySpaceIDError{
		Kind:    ExtractKeySpaceIDParseError,
		Message: fmt.Sprintf("No annotation found in '%v'", sql),
	}
	return
}

// Extracts the string from source contained between the leftmost instance of
// 'leftDelim' and the next instance of 'rightDelim'. If there is no next instance
// of 'rightDelim', returns the string contained between the end of the leftmost instance
// of 'leftDelim' to the end of 'source'. If 'leftDelim' does not appear in 'source',
// sets 'found' to false and 'match' to the empty string, otherwise 'found' is set to true
// and 'match' is set to the extracted string.
func extractStringBetween(source string, leftDelim string, rightDelim string) (match string, found bool) {
	leftDelimStart := strings.Index(source, leftDelim)
	if leftDelimStart == -1 {
		found = false
		match = ""
		return
	}
	found = true
	matchStart := leftDelimStart + len(leftDelim)
	matchEnd := strings.Index(source[matchStart:], rightDelim)
	if matchEnd != -1 {
		match = source[matchStart : matchStart+matchEnd]
		return
	}
	match = source[matchStart:]
	return
}

// ExtractKeySpaceIDError is the error type returned
// from ExtractKeySpaceID
// Kind is a numeric code for the error (see constants below)
// and Message is an error message string.
type ExtractKeySpaceIDError struct {
	Kind    int
	Message string
}

// Possible values for ExtractKeySpaceIDError.Kind
const (
	ExtractKeySpaceIDParseError                 = iota
	ExtractKeySpaceIDReplicationUnfriendlyError = iota
)

func (err ExtractKeySpaceIDError) Error() string {
	switch err.Kind {
	case ExtractKeySpaceIDParseError:
		return fmt.Sprintf("Parse-Error. %v", err.Message)
	case ExtractKeySpaceIDReplicationUnfriendlyError:
		return fmt.Sprintf(
			"Statement is filtered-replication-unfriendly. %v", err.Message)
	default:
		log.Fatalf("Unknown error type: %v", err)
		return "" // Unreachable.
	}
}
