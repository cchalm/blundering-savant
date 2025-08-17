// Just test if taskgen.go compiles
package main

import (
	"os"
	"os/exec"
	"log"
)

func main() {
	cmd := exec.Command("go", "build", "./cmd/blundering-savant/taskgen.go")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Build error: %v", err)
		log.Printf("Output: %s", output)
	} else {
		log.Printf("Build successful")
	}
}