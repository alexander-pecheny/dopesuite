package main

import (
	"flag"
	"slices"
	"testing"
)

// The usage table and the spec are written out separately — one groups commands
// for a reader, the other lists them for the GUI — so they are checked against
// each other here.
func TestSpecAndUsageListTheSameCommands(t *testing.T) {
	var usage []string
	for _, c := range commands {
		if c.verb != "" {
			usage = append(usage, expandVerb(c.verb)...)
		}
	}
	var spec []string
	for _, r := range runnables {
		spec = append(spec, r.verb)
	}
	for _, v := range usage {
		if !slices.Contains(spec, v) {
			t.Errorf("%q is in the usage table and not in runnables", v)
		}
	}
	for _, v := range spec {
		if !slices.Contains(usage, v) {
			t.Errorf("%q is in runnables and not in the usage table", v)
		}
	}
}

func TestEveryCommandDeclaresItsFlags(t *testing.T) {
	for _, r := range runnables {
		flags, err := flagsOf(r.verb, r.run)
		if err != nil {
			t.Errorf("%s: %v", r.verb, err)
			continue
		}
		if len(flags) == 0 {
			t.Errorf("%s: no flags at all", r.verb)
		}
	}
}

// A choice the GUI offers has to be one the command accepts, and the default
// has to be among them.
func TestChoicesContainTheDefault(t *testing.T) {
	for _, r := range runnables {
		flags, err := flagsOf(r.verb, r.run)
		if err != nil {
			continue
		}
		for _, f := range flags {
			if len(f.Choices) == 0 || f.Default == "" {
				continue
			}
			if !slices.Contains(f.Choices, f.Default) {
				t.Errorf("%s --%s: default %q is not one of %v", r.verb, f.Name, f.Default, f.Choices)
			}
		}
	}
}

// A flag folded away under "Advanced" by a name no command declares any more is
// a flag that quietly came back to the front of the form.
func TestAdvancedNamesAreRealFlags(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range runnables {
		flags, err := flagsOf(r.verb, r.run)
		if err != nil {
			t.Fatalf("%s: %v", r.verb, err)
		}
		for _, f := range flags {
			declared[f.Name] = true
		}
	}
	for name := range advanced {
		if !declared[name] {
			t.Errorf("advanced names --%s, which no command declares", name)
		}
	}
	for verb, flags := range advancedHere {
		for name := range flags {
			if !declared[name] {
				t.Errorf("advancedHere[%q] names --%s, which no command declares", verb, name)
			}
		}
	}
}

// The GUI draws the flags in the order they are declared, which only works
// while every way of declaring one goes through flagSet's own methods. A flag
// declared straight on the embedded FlagSet would go missing from the form.
func TestEveryFlagIsInTheDeclaredOrder(t *testing.T) {
	for _, r := range runnables {
		var set *flagSet
		collector = func(fs *flagSet) { set = fs }
		_ = r.run(nil)
		collector = nil
		counted := 0
		set.VisitAll(func(*flag.Flag) { counted++ })
		if counted != len(set.order) {
			t.Errorf("%s: %d flags declared, %d in order", r.verb, counted, len(set.order))
		}
	}
}
