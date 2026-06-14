package common

import "strings"

func has0xPrefix(input string) bool {
	return len(input) >= 2 && input[0] == '0' && (input[1] == 'x' || input[1] == 'X')
}

func ExistsIgnoreCase(set []string, find string) bool {
	find = strings.ToLower(find)
	for _, s := range set {
		if strings.ToLower(s) == find {
			return true
		}
	}
	return false
}
