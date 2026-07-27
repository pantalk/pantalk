// Package procenv builds the environment for the child processes Pantalk
// launches for agents.
package procenv

import (
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Apply sets a command's environment to exactly the supplied entries.
//
// Child processes inherit nothing from the daemon by default. The daemon's
// environment holds every bot credential a config resolved through $NAME, so
// inheriting it would hand each agent the tokens of every service Pantalk
// serves. An agent receives a variable only when its definition names one,
// either as an explicit env entry or through env_inherit.
//
// The environment is always set, never left nil, so an agent with no
// configured entries runs with an empty environment rather than falling back
// to exec's inheriting default. Entries are sorted, so repeated invocations
// produce an identical environment.
func Apply(cmd *exec.Cmd, entries map[string]string) {
	if cmd == nil {
		return
	}

	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+entries[key])
	}
	cmd.Env = env
}

// Inherit copies the named variables from the daemon's environment. Names that
// are unset are skipped, so an optional passthrough does not have to exist on
// every host. A name may be written with a trailing "*" to copy every variable
// sharing that prefix, which keeps families such as LC_* to one entry.
func Inherit(names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}

	var prefixes []string
	inherited := make(map[string]string, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, "*") {
			prefixes = append(prefixes, strings.TrimSuffix(name, "*"))
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			inherited[name] = value
		}
	}

	if len(prefixes) > 0 {
		for _, entry := range os.Environ() {
			key, value, found := strings.Cut(entry, "=")
			if !found {
				continue
			}
			for _, prefix := range prefixes {
				if strings.HasPrefix(key, prefix) {
					inherited[key] = value
					break
				}
			}
		}
	}

	if len(inherited) == 0 {
		return nil
	}
	return inherited
}
