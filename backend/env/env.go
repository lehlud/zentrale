package env

import "os"

var defaults = map[string]string{}

func SetDefault(name string, value string) {
	defaults[name] = value
}

func GetVar(name string) string {
	val, exists := os.LookupEnv(name)
	if !exists {
		return defaults[name]
	}

	return val
}
