package runner

import (
	"encoding/json"
	"fmt"

	"github.com/stonith404/umpteenth/backend/internal/playbook"
)

// ApplyPlaybook sets everything a run takes from the job's playbook, from a saved version or one reflection is about to save
func (j *JobConfig) ApplyPlaybook(c playbook.Content) error {
	verify, err := playbook.ParseVerify(c.Verify)
	if err != nil {
		return fmt.Errorf("invalid verify checks: %w", err)
	}
	j.Verify = verify
	j.PlaybookRendered = c.Render()
	j.PlaybookMarkdown = c.Markdown()
	j.Setup, j.Main, j.Dockerfile, j.DockerfileHash = "", "", "", ""
	if c.Setup != nil {
		j.Setup = *c.Setup
	}
	if c.Main != nil {
		j.Main = *c.Main
	}
	if c.Dockerfile != nil {
		j.Dockerfile = *c.Dockerfile
		j.DockerfileHash = c.DockerfileHash()
	}

	j.Toolkit = nil
	for _, s := range c.Toolkit {
		script := ToolkitScript{Name: s.Name, Content: s.Content, Description: s.Description, SideEffects: s.SideEffects}
		// Args that don't parse leave the tool without arguments, which the agent can still call
		if len(s.Args) > 0 {
			_ = json.Unmarshal(s.Args, &script.Args)
		}
		j.Toolkit = append(j.Toolkit, script)
	}
	return nil
}
