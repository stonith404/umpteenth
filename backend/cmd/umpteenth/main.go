package main

import (
	_ "time/tzdata"

	"github.com/stonith404/umpteenth/backend/internal/cmds"
)

func main() {
	cmds.Execute()
}
