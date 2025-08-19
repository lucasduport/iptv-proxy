package utils

import (
	"fmt"
	"os"
)

// PrintEnv prints the current environment variables
func PrintEnv() {
	for _, e := range os.Environ() {
		fmt.Println(e)
	}
}