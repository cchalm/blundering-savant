package main

import (
	"fmt"
	"log"
	"os/exec"
)

func main() {
	// Test just the compilation
	cmd := exec.Command("go", "test", "-c", "./cmd/blundering-savant")
	output, err := cmd.CombinedOutput()
	fmt.Printf("Output: %s\n", output)
	if err != nil {
		log.Printf("Test compilation error: %v", err)
	} else {
		log.Printf("Test compilation successful")
	}
}