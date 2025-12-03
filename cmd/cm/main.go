package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("cm - Claude Manager")
		os.Exit(0)
	}
	fmt.Printf("Unknown command: %s\n", os.Args[1])
	os.Exit(1)
}
