package skills

import "fmt"

// Source returns a skill's SKILL.md exactly as shipped, frontmatter included. Hosts that serve skills verbatim (the MCP
// server's skill:// resources) need the raw file: Describe parses it, and a parse drops frontmatter fields the host must
// pass through unchanged.
func Source(name string) ([]byte, error) {
	name, _ = Resolve(name)
	b, err := docs.ReadFile(name + "/SKILL.md")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	return b, nil
}
