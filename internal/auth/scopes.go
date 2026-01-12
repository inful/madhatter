package auth

import "strings"

func parseScopes(scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil
	}

	// Support both space and comma separated scopes.
	scope = strings.ReplaceAll(scope, ",", " ")
	return strings.Fields(scope)
}
