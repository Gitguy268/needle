package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Presets are the named themes, chosen with `name` under [theme] or --theme.
// Both are unofficial tributes; NOTICE says where the colours and words come from.
var Presets = map[string]Theme{
	"nujabes": {
		Name: "nujabes", Background: "#0d0a11", Foreground: "#e3dcda",
		Accent: "#b26ac6", Dim: "#8a8187", Select: "#e88b2c",
		Quotes: []string{"Your beat still laments the world."},
	},
	"graduation": {
		Name: "graduation", Background: "#1a0b2e", Foreground: "#fff4e0",
		Accent: "#ff5fa8", Dim: "#9a86b8", Select: "#ffc93c",
		Quotes: graduationQuotes,
	},
}

// ResolveTheme returns a preset, or today's default look for "default".
func ResolveTheme(name string) (Theme, error) {
	if name == "default" {
		return Default().Theme, nil
	}
	if t, ok := Presets[name]; ok {
		return t, nil
	}
	names := []string{"default"}
	for n := range Presets {
		names = append(names, n)
	}
	slices.Sort(names)
	return Theme{}, fmt.Errorf("unknown theme %q (have %s)", name, strings.Join(names, ", "))
}

// applyPreset starts from the named preset and keeps only the colours the
// config file sets itself.
func applyPreset(file Theme, md toml.MetaData) (Theme, error) {
	t, err := ResolveTheme(file.Name)
	if err != nil {
		return file, err
	}
	for _, c := range []struct {
		key  string
		from string
		to   *string
	}{
		{"background", file.Background, &t.Background},
		{"foreground", file.Foreground, &t.Foreground},
		{"accent", file.Accent, &t.Accent},
		{"text", file.Text, &t.Text},
		{"dim", file.Dim, &t.Dim},
		{"select", file.Select, &t.Select},
	} {
		if md.IsDefined("theme", c.key) {
			*c.to = c.from
		}
	}
	return t, nil
}

// graduationQuotes is a curated part of the kanye.rest list.
var graduationQuotes = []string{
	"All you have to be is yourself",
	"Believe in your flyness...conquer your shyness.",
	"Distraction is the enemy of vision",
	"Everything you do in life stems from either fear or love",
	"For me giving up is way harder than trying.",
	"For me, money is not my definition of success. Inspiring people is a definition of success",
	"I feel calm but energized",
	"I feel like I'm too busy writing history to read it.",
	"I love sleep; it's my favorite.",
	"I make awesome decisions in bike stores!!!",
	"I want the world to be better! All I want is positive! All I want is dopeness!",
	"I wish I had a friend like me",
	"I'll say things that are serious and put them in a joke form so people can enjoy them. We laugh to keep from crying.",
	"I'm nice at ping pong",
	"If I got any cooler I would freeze to death",
	"If you have the opportunity to play this game of life you need to appreciate every moment. a lot of people don't appreciate the moment until it's passed.",
	"Keep squares out yo circle",
	"Let's be like water",
	"Man... whatever happened to my antique fish tank?",
	"My greatest award is what I'm about to do.",
	"My greatest pain in life is that I will never be able to see myself perform live.",
	"People always tell you 'Be humble. Be humble.' When was the last time someone told you to be amazing? Be great! Be awesome! Be awesome!",
	"People only get jealous when they care.",
	"Sometimes I push the door close button on people running towards the elevator. I just need my own elevator sometimes. My sanctuary.",
	"Sometimes you have to get rid of everything",
	"Style is genderless",
	"The world is our family",
	"The world is our office",
	"Today is the best day ever and tomorrow's going to be even better",
	"Tweeting is legal and also therapeutic",
	"We all self-conscious. I'm just the first to admit it.",
	"We came into a broken world. And we're the cleanup crew.",
	"You can't look at a glass half full or empty if it's overflowing.",
	"I hate when I'm on a flight and I wake up with a water bottle next to me like oh great now I gotta be responsible for this water bottle",
	"I watch Bladerunner on repeat",
	"Life is the ultimate gift",
	"Manga all day",
	"My memories are from the future",
	"There's so many lonely emojis man",
	"Trust me ... I won't stop",
	"We have to evolve",
	"Winning is the only option",
	"I've known my mom since I was zero years old. She is quite dope.",
	"I'm on the pursuit of awesomeness, excellence is the bare minimum.",
	"You basically can say anything to someone on an email or text as long as you put LOL at the end.",
}
