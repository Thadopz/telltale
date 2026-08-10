package sqlobserve

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const defaultGuardedReadLimit = 100

var (
	allowedReadStatementPattern  = regexp.MustCompile(`(?i)^(select|show|explain)\b`)
	forbiddenReadKeywordPattern  = regexp.MustCompile(`(?i)\b(?:insert|update|delete|drop|alter|create|truncate|replace|grant|revoke|call|set|commit|rollback|start|begin|lock|unlock|load|outfile|dumpfile)\b`)
	dangerousReadFunctionPattern = regexp.MustCompile(`(?i)\b(?:sleep|benchmark|load_file|get_lock|release_lock)\s*\(`)
	lockingReadPattern           = regexp.MustCompile(`(?i)\b(?:for\s+update|lock\s+in\s+share\s+mode)\b`)
)

// GuardMySQLReadOnly accepts a deliberately small subset of MySQL statements.
// It rejects read-shaped statements that can write files, hold locks, or spend
// unbounded CPU time, and it applies a limit to the outer SELECT result.
func GuardMySQLReadOnly(sql string, maxRows int) (string, error) {
	trimmed := strings.TrimSpace(sql)
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, ";"))
	if trimmed == "" {
		return "", fmt.Errorf("empty sql")
	}
	if strings.Contains(trimmed, ";") {
		return "", fmt.Errorf("multiple mysql statements are not allowed")
	}

	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "--") || strings.Contains(lower, "/*") || strings.Contains(lower, "*/") || strings.Contains(lower, "#") {
		return "", fmt.Errorf("sql comments are not allowed")
	}
	if !allowedReadStatementPattern.MatchString(trimmed) {
		return "", fmt.Errorf("only SELECT, SHOW, and EXPLAIN statements are allowed")
	}
	if forbiddenReadKeywordPattern.MatchString(trimmed) ||
		dangerousReadFunctionPattern.MatchString(trimmed) ||
		lockingReadPattern.MatchString(trimmed) ||
		strings.Contains(trimmed, ":=") {
		return "", fmt.Errorf("sql contains a forbidden read-side effect")
	}

	if !strings.EqualFold(firstSQLKeyword(trimmed), "select") {
		return trimmed, nil
	}
	if maxRows <= 0 {
		maxRows = defaultGuardedReadLimit
	}
	return applyTopLevelSelectLimit(trimmed, maxRows)
}

func firstSQLKeyword(sql string) string {
	end := 0
	for end < len(sql) && isSQLWordByte(sql[end]) {
		end++
	}
	return sql[:end]
}

func applyTopLevelSelectLimit(sql string, maxRows int) (string, error) {
	start, end, found, err := topLevelLimitCountRange(sql)
	if err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("%s LIMIT %d", sql, maxRows), nil
	}

	currentLimit, err := strconv.Atoi(sql[start:end])
	if err != nil {
		return "", fmt.Errorf("SELECT LIMIT must be a positive integer")
	}
	if currentLimit <= maxRows {
		return sql, nil
	}
	return sql[:start] + strconv.Itoa(maxRows) + sql[end:], nil
}

func topLevelLimitCountRange(sql string) (start int, end int, found bool, err error) {
	depth := 0
	var quote byte

	for index := 0; index < len(sql); index++ {
		character := sql[index]
		if quote != 0 {
			if character == '\\' && index+1 < len(sql) {
				index++
				continue
			}
			if character == quote {
				if index+1 < len(sql) && sql[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}

		switch character {
		case '\'', '"', '`':
			quote = character
			continue
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || !isSQLWordByte(character) {
			continue
		}

		wordStart := index
		for index < len(sql) && isSQLWordByte(sql[index]) {
			index++
		}
		word := sql[wordStart:index]
		if !strings.EqualFold(word, "limit") {
			index--
			continue
		}
		return parseLimitCountRange(sql, index)
	}

	return 0, 0, false, nil
}

func parseLimitCountRange(sql string, index int) (start int, end int, found bool, err error) {
	index = skipSQLSpaces(sql, index)
	firstStart := index
	for index < len(sql) && sql[index] >= '0' && sql[index] <= '9' {
		index++
	}
	if firstStart == index {
		return 0, 0, false, fmt.Errorf("SELECT LIMIT must be a positive integer")
	}
	firstEnd := index

	index = skipSQLSpaces(sql, index)
	if index >= len(sql) || sql[index] != ',' {
		return firstStart, firstEnd, true, nil
	}

	index = skipSQLSpaces(sql, index+1)
	secondStart := index
	for index < len(sql) && sql[index] >= '0' && sql[index] <= '9' {
		index++
	}
	if secondStart == index {
		return 0, 0, false, fmt.Errorf("SELECT LIMIT row count must be a positive integer")
	}
	return secondStart, index, true, nil
}

func skipSQLSpaces(sql string, index int) int {
	for index < len(sql) {
		switch sql[index] {
		case ' ', '\t', '\r', '\n':
			index++
		default:
			return index
		}
	}
	return index
}

func isSQLWordByte(character byte) bool {
	return (character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z') ||
		(character >= '0' && character <= '9') ||
		character == '_'
}
