package wasi

import (
	"os"
	"path/filepath"
	"strings"
)

// Rule describes which HTTP routes a middleware module applies to.
// Loaded from a module's rule.txt at startup.
type Rule struct {
	All    bool
	Only   []string // apply only to these route names
	Except []string // apply to all except these route names
}

// parseRule parses the content of rule.txt.
//
//	"*" or ""     → Rule{All: true}
//	"users,auth"  → Rule{Only: ["users","auth"]}
//	"-auth"       → Rule{All: true, Except: ["auth"]}
func parseRule(content string) Rule {
	content = strings.TrimSpace(content)
	if content == "*" || content == "" {
		return Rule{All: true}
	}

	r := Rule{}
	parts := strings.Split(content, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "-") {
			r.All = true
			r.Except = append(r.Except, p[1:])
		} else {
			r.Only = append(r.Only, p)
		}
	}
	return r
}

// MiddlewareModule pairs a Module with its routing Rule.
type MiddlewareModule struct {
	Module *Module
	Rule   Rule
}

// Matches reports whether this middleware applies to a given route name.
func (mw *MiddlewareModule) Matches(routeID string) bool {
	if mw.Rule.All {
		for _, ex := range mw.Rule.Except {
			if ex == routeID {
				return false
			}
		}
		return true
	}

	for _, o := range mw.Rule.Only {
		if o == routeID {
			return true
		}
	}
	return false
}

// applyPipeline returns middlewares applicable to route, in registration order.
func applyPipeline(route string, middlewares []*MiddlewareModule) []*MiddlewareModule {
	var pipeline []*MiddlewareModule
	for _, mw := range middlewares {
		if mw.Matches(route) {
			pipeline = append(pipeline, mw)
		}
	}
	return pipeline
}

// loadRuleFromSourceDir reads modulesDir/<name>/rule.txt.
// Returns (Rule{}, false) if absent — module is not a middleware.
func loadRuleFromSourceDir(modulesDir, name string) (Rule, bool) {
	rulePath := filepath.Join(modulesDir, name, "rule.txt")
	content, err := os.ReadFile(rulePath)
	if err != nil {
		return Rule{}, false
	}
	return parseRule(string(content)), true
}
