package styles

import "charm.land/lipgloss/v2"

// This file holds the built-in palettes added after the original three. Two of
// them borrow another product's identity (Claude, GitHub); the rest are
// original. Each palette fills every role, because quickStyle derives the whole
// Styles struct from these tokens and a token left unset inherits the dark
// Charmtone assumption.

// claudeOpts returns a warm, paper-toned dark palette in the spirit of
// Anthropic's Claude: cream text on warm near-black, with terracotta as the
// single loud color.
func claudeOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#d97757"), // terracotta
		secondary: lipgloss.Color("#d2a27e"), // sand
		accent:    lipgloss.Color("#9db88c"), // sage
		keyword:   lipgloss.Color("#d8a657"), // amber

		fgBase:       lipgloss.Color("#ede9de"), // paper
		fgMoreSubtle: lipgloss.Color("#a8a294"),
		fgSubtle:     lipgloss.Color("#c9c3b4"),
		fgMostSubtle: lipgloss.Color("#837e72"),

		onPrimary: lipgloss.Color("#1a1915"),

		bgBase:         lipgloss.Color("#1a1915"),
		bgLeastVisible: lipgloss.Color("#242320"),
		bgLessVisible:  lipgloss.Color("#2e2c28"),
		bgMostVisible:  lipgloss.Color("#3d3a34"),

		separator: lipgloss.Color("#2e2c28"),

		destructive:       lipgloss.Color("#e87a6b"),
		error:             lipgloss.Color("#c15b50"),
		warningSubtle:     lipgloss.Color("#e8c07d"),
		warning:           lipgloss.Color("#c99b4e"),
		attention:         lipgloss.Color("#e0895c"),
		busy:              lipgloss.Color("#d8a657"),
		info:              lipgloss.Color("#7fa9c9"),
		infoMoreSubtle:    lipgloss.Color("#6b92b0"),
		infoMostSubtle:    lipgloss.Color("#4f728c"),
		success:           lipgloss.Color("#8fb573"),
		successMoreSubtle: lipgloss.Color("#7ca163"),
		successMostSubtle: lipgloss.Color("#6b8c55"),

		yolo:           lipgloss.Color("#d8a657"),
		plan:           lipgloss.Color("#d97757"),
		planMoreSubtle: lipgloss.Color("#5a453c"),

		button:         lipgloss.Color("#d97757"),
		buttonSubtle:   lipgloss.Color("#2e2c28"),
		buttonInactive: lipgloss.Color("#3d3a34"),
		buttonHovered:  lipgloss.Color("#a8a294"),

		ansiBlack:   lipgloss.Color("#242320"),
		ansiRed:     lipgloss.Color("#e87a6b"),
		ansiGreen:   lipgloss.Color("#8fb573"),
		ansiYellow:  lipgloss.Color("#d8a657"),
		ansiBlue:    lipgloss.Color("#7fa9c9"),
		ansiMagenta: lipgloss.Color("#c08fc4"),
		ansiCyan:    lipgloss.Color("#84b8b0"),
		ansiWhite:   lipgloss.Color("#c9c3b4"),

		ansiBrightBlack:   lipgloss.Color("#3d3a34"),
		ansiBrightRed:     lipgloss.Color("#f2948a"),
		ansiBrightGreen:   lipgloss.Color("#a8cc8c"),
		ansiBrightYellow:  lipgloss.Color("#e8c07d"),
		ansiBrightBlue:    lipgloss.Color("#9dc0da"),
		ansiBrightMagenta: lipgloss.Color("#d6a8d8"),
		ansiBrightCyan:    lipgloss.Color("#a0cfc7"),
		ansiBrightWhite:   lipgloss.Color("#f5f2ea"),
	}
}

// githubOpts returns GitHub's dark palette: flat near-black surfaces, a blue
// for interactive things, and the green/red pair GitHub uses for diffs.
func githubOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#58a6ff"), // github blue
		secondary: lipgloss.Color("#bc8cff"), // purple
		accent:    lipgloss.Color("#3fb950"), // green
		keyword:   lipgloss.Color("#ff7b72"), // red

		fgBase:       lipgloss.Color("#c9d1d9"),
		fgMoreSubtle: lipgloss.Color("#8b949e"),
		fgSubtle:     lipgloss.Color("#b1bac4"),
		fgMostSubtle: lipgloss.Color("#6e7681"),

		onPrimary: lipgloss.Color("#0d1117"),

		bgBase:         lipgloss.Color("#0d1117"),
		bgLeastVisible: lipgloss.Color("#161b22"),
		bgLessVisible:  lipgloss.Color("#21262d"),
		bgMostVisible:  lipgloss.Color("#30363d"),

		separator: lipgloss.Color("#21262d"),

		destructive:       lipgloss.Color("#f85149"),
		error:             lipgloss.Color("#da3633"),
		warningSubtle:     lipgloss.Color("#e3b341"),
		warning:           lipgloss.Color("#d29922"),
		attention:         lipgloss.Color("#db6d28"),
		busy:              lipgloss.Color("#e3b341"),
		info:              lipgloss.Color("#58a6ff"),
		infoMoreSubtle:    lipgloss.Color("#388bfd"),
		infoMostSubtle:    lipgloss.Color("#1f6feb"),
		success:           lipgloss.Color("#3fb950"),
		successMoreSubtle: lipgloss.Color("#2ea043"),
		successMostSubtle: lipgloss.Color("#238636"),

		yolo:           lipgloss.Color("#e3b341"),
		plan:           lipgloss.Color("#58a6ff"),
		planMoreSubtle: lipgloss.Color("#1f3a5f"),

		button:         lipgloss.Color("#1f6feb"),
		buttonSubtle:   lipgloss.Color("#21262d"),
		buttonInactive: lipgloss.Color("#30363d"),
		buttonHovered:  lipgloss.Color("#58a6ff"),

		ansiBlack:   lipgloss.Color("#161b22"),
		ansiRed:     lipgloss.Color("#f85149"),
		ansiGreen:   lipgloss.Color("#3fb950"),
		ansiYellow:  lipgloss.Color("#d29922"),
		ansiBlue:    lipgloss.Color("#58a6ff"),
		ansiMagenta: lipgloss.Color("#bc8cff"),
		ansiCyan:    lipgloss.Color("#39c5cf"),
		ansiWhite:   lipgloss.Color("#b1bac4"),

		ansiBrightBlack:   lipgloss.Color("#6e7681"),
		ansiBrightRed:     lipgloss.Color("#ff7b72"),
		ansiBrightGreen:   lipgloss.Color("#56d364"),
		ansiBrightYellow:  lipgloss.Color("#e3b341"),
		ansiBrightBlue:    lipgloss.Color("#79c0ff"),
		ansiBrightMagenta: lipgloss.Color("#d2a8ff"),
		ansiBrightCyan:    lipgloss.Color("#56d4dd"),
		ansiBrightWhite:   lipgloss.Color("#f0f6fc"),
	}
}

// nocturneOpts returns an original theme: a deep indigo room lit by a single
// gold lamp, with violet in the shadows and aqua for anything that succeeded.
func nocturneOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#e0b15c"), // gold
		secondary: lipgloss.Color("#8e7bd6"), // violet
		accent:    lipgloss.Color("#6fd3c7"), // aqua
		keyword:   lipgloss.Color("#e08ba0"), // rose

		fgBase:       lipgloss.Color("#e6e4f5"),
		fgMoreSubtle: lipgloss.Color("#9a96b8"),
		fgSubtle:     lipgloss.Color("#c3bfdc"),
		fgMostSubtle: lipgloss.Color("#706c8e"),

		onPrimary: lipgloss.Color("#131327"),

		bgBase:         lipgloss.Color("#131327"),
		bgLeastVisible: lipgloss.Color("#1b1b33"),
		bgLessVisible:  lipgloss.Color("#242441"),
		bgMostVisible:  lipgloss.Color("#33334f"),

		separator: lipgloss.Color("#242441"),

		destructive:       lipgloss.Color("#e8657d"),
		error:             lipgloss.Color("#c94a63"),
		warningSubtle:     lipgloss.Color("#efc98b"),
		warning:           lipgloss.Color("#c9a05c"),
		attention:         lipgloss.Color("#e89a5c"),
		busy:              lipgloss.Color("#e0b15c"),
		info:              lipgloss.Color("#7fb2f0"),
		infoMoreSubtle:    lipgloss.Color("#6294d6"),
		infoMostSubtle:    lipgloss.Color("#4a76b8"),
		success:           lipgloss.Color("#6fd3a0"),
		successMoreSubtle: lipgloss.Color("#58b88a"),
		successMostSubtle: lipgloss.Color("#479a72"),

		yolo:           lipgloss.Color("#e0b15c"),
		plan:           lipgloss.Color("#e0b15c"),
		planMoreSubtle: lipgloss.Color("#3b3560"),

		button:         lipgloss.Color("#8e7bd6"),
		buttonSubtle:   lipgloss.Color("#242441"),
		buttonInactive: lipgloss.Color("#33334f"),
		buttonHovered:  lipgloss.Color("#9a96b8"),

		ansiBlack:   lipgloss.Color("#1b1b33"),
		ansiRed:     lipgloss.Color("#e8657d"),
		ansiGreen:   lipgloss.Color("#6fd3a0"),
		ansiYellow:  lipgloss.Color("#e0b15c"),
		ansiBlue:    lipgloss.Color("#7fb2f0"),
		ansiMagenta: lipgloss.Color("#b79bee"),
		ansiCyan:    lipgloss.Color("#6fd3c7"),
		ansiWhite:   lipgloss.Color("#c3bfdc"),

		ansiBrightBlack:   lipgloss.Color("#33334f"),
		ansiBrightRed:     lipgloss.Color("#f08a9c"),
		ansiBrightGreen:   lipgloss.Color("#8be3b8"),
		ansiBrightYellow:  lipgloss.Color("#f0cd8f"),
		ansiBrightBlue:    lipgloss.Color("#9fc7f5"),
		ansiBrightMagenta: lipgloss.Color("#cbb2f5"),
		ansiBrightCyan:    lipgloss.Color("#8fe0d6"),
		ansiBrightWhite:   lipgloss.Color("#f2f1fb"),
	}
}

// abyssOpts returns an original theme: the black of deep water, lit only by
// the cold bioluminescence of the things that live there.
func abyssOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#35e0c8"), // bioluminescent aqua
		secondary: lipgloss.Color("#5aa9ff"), // cold blue
		accent:    lipgloss.Color("#8af0d0"), // pale glow
		keyword:   lipgloss.Color("#79c7ff"),

		fgBase:       lipgloss.Color("#c6e4ea"),
		fgMoreSubtle: lipgloss.Color("#7096a0"),
		fgSubtle:     lipgloss.Color("#9fc3cc"),
		fgMostSubtle: lipgloss.Color("#4e6e78"),

		onPrimary: lipgloss.Color("#04100f"),

		bgBase:         lipgloss.Color("#05080c"),
		bgLeastVisible: lipgloss.Color("#0a1017"),
		bgLessVisible:  lipgloss.Color("#101a24"),
		bgMostVisible:  lipgloss.Color("#1a2a38"),

		separator: lipgloss.Color("#101a24"),

		destructive:       lipgloss.Color("#ff6b7a"),
		error:             lipgloss.Color("#d34c5c"),
		warningSubtle:     lipgloss.Color("#b6e8a0"),
		warning:           lipgloss.Color("#86c77a"),
		attention:         lipgloss.Color("#ffb86b"),
		busy:              lipgloss.Color("#8af0d0"),
		info:              lipgloss.Color("#5aa9ff"),
		infoMoreSubtle:    lipgloss.Color("#4a8cd6"),
		infoMostSubtle:    lipgloss.Color("#3a6da8"),
		success:           lipgloss.Color("#35e0c8"),
		successMoreSubtle: lipgloss.Color("#2bb89f"),
		successMostSubtle: lipgloss.Color("#1f907e"),

		yolo:           lipgloss.Color("#b6e8a0"),
		plan:           lipgloss.Color("#35e0c8"),
		planMoreSubtle: lipgloss.Color("#10333a"),

		button:         lipgloss.Color("#35e0c8"),
		buttonSubtle:   lipgloss.Color("#101a24"),
		buttonInactive: lipgloss.Color("#1a2a38"),
		buttonHovered:  lipgloss.Color("#7096a0"),

		ansiBlack:   lipgloss.Color("#0a1017"),
		ansiRed:     lipgloss.Color("#ff6b7a"),
		ansiGreen:   lipgloss.Color("#35e0c8"),
		ansiYellow:  lipgloss.Color("#b6e8a0"),
		ansiBlue:    lipgloss.Color("#5aa9ff"),
		ansiMagenta: lipgloss.Color("#a68cff"),
		ansiCyan:    lipgloss.Color("#8af0d0"),
		ansiWhite:   lipgloss.Color("#9fc3cc"),

		ansiBrightBlack:   lipgloss.Color("#1a2a38"),
		ansiBrightRed:     lipgloss.Color("#ff9099"),
		ansiBrightGreen:   lipgloss.Color("#5ff0dc"),
		ansiBrightYellow:  lipgloss.Color("#cdf0bc"),
		ansiBrightBlue:    lipgloss.Color("#8cc4ff"),
		ansiBrightMagenta: lipgloss.Color("#c2aeff"),
		ansiBrightCyan:    lipgloss.Color("#b0fbe8"),
		ansiBrightWhite:   lipgloss.Color("#e4f7fa"),
	}
}

// emberOpts returns an original theme: charcoal and the last light of a fire,
// amber through coral, with ash-grey for everything at rest.
func emberOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#ff9e4a"), // amber
		secondary: lipgloss.Color("#e8695c"), // coral
		accent:    lipgloss.Color("#ffc46b"), // ember light
		keyword:   lipgloss.Color("#f2c879"),

		fgBase:       lipgloss.Color("#f0e4d4"),
		fgMoreSubtle: lipgloss.Color("#a79a86"),
		fgSubtle:     lipgloss.Color("#cfc0ac"),
		fgMostSubtle: lipgloss.Color("#7e7462"),

		onPrimary: lipgloss.Color("#1a1308"),

		bgBase:         lipgloss.Color("#17130f"),
		bgLeastVisible: lipgloss.Color("#201a14"),
		bgLessVisible:  lipgloss.Color("#2b231a"),
		bgMostVisible:  lipgloss.Color("#3a3024"),

		separator: lipgloss.Color("#2b231a"),

		destructive:       lipgloss.Color("#f0614f"),
		error:             lipgloss.Color("#cf4a3a"),
		warningSubtle:     lipgloss.Color("#f5cd85"),
		warning:           lipgloss.Color("#d4a94f"),
		attention:         lipgloss.Color("#ffb067"),
		busy:              lipgloss.Color("#ffc46b"),
		info:              lipgloss.Color("#7fb8d6"),
		infoMoreSubtle:    lipgloss.Color("#6598b8"),
		infoMostSubtle:    lipgloss.Color("#4b7894"),
		success:           lipgloss.Color("#a8c97a"),
		successMoreSubtle: lipgloss.Color("#8fb366"),
		successMostSubtle: lipgloss.Color("#749452"),

		yolo:           lipgloss.Color("#ffc46b"),
		plan:           lipgloss.Color("#ff9e4a"),
		planMoreSubtle: lipgloss.Color("#4a3524"),

		button:         lipgloss.Color("#ff9e4a"),
		buttonSubtle:   lipgloss.Color("#2b231a"),
		buttonInactive: lipgloss.Color("#3a3024"),
		buttonHovered:  lipgloss.Color("#a79a86"),

		ansiBlack:   lipgloss.Color("#201a14"),
		ansiRed:     lipgloss.Color("#f0614f"),
		ansiGreen:   lipgloss.Color("#a8c97a"),
		ansiYellow:  lipgloss.Color("#d4a94f"),
		ansiBlue:    lipgloss.Color("#7fb8d6"),
		ansiMagenta: lipgloss.Color("#d69ac4"),
		ansiCyan:    lipgloss.Color("#8fd0c4"),
		ansiWhite:   lipgloss.Color("#cfc0ac"),

		ansiBrightBlack:   lipgloss.Color("#3a3024"),
		ansiBrightRed:     lipgloss.Color("#ff8a78"),
		ansiBrightGreen:   lipgloss.Color("#c4e397"),
		ansiBrightYellow:  lipgloss.Color("#ffd98f"),
		ansiBrightBlue:    lipgloss.Color("#9fd2ee"),
		ansiBrightMagenta: lipgloss.Color("#ecb4d8"),
		ansiBrightCyan:    lipgloss.Color("#a8e6da"),
		ansiBrightWhite:   lipgloss.Color("#fbf3e8"),
	}
}

// papercutOpts returns an original light theme: warm paper stock, black ink,
// and vermilion for the one thing that needs to be found immediately.
func papercutOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#c2412a"), // vermilion
		secondary: lipgloss.Color("#2f5d62"), // deep teal ink
		accent:    lipgloss.Color("#4a7a50"), // ink green
		keyword:   lipgloss.Color("#8c3a6b"), // plum

		fgBase:       lipgloss.Color("#221f1a"),
		fgMoreSubtle: lipgloss.Color("#5c554a"),
		fgSubtle:     lipgloss.Color("#3d3830"),
		fgMostSubtle: lipgloss.Color("#837a6c"),

		onPrimary: lipgloss.Color("#fbf7f0"),

		bgBase:         lipgloss.Color("#f7f3ea"),
		bgLeastVisible: lipgloss.Color("#efe9dc"),
		bgLessVisible:  lipgloss.Color("#e3daca"),
		bgMostVisible:  lipgloss.Color("#cfc4af"),

		separator: lipgloss.Color("#d9ccb6"),

		destructive:       lipgloss.Color("#b02a1c"),
		error:             lipgloss.Color("#8f2418"),
		warningSubtle:     lipgloss.Color("#6e4f12"),
		warning:           lipgloss.Color("#a8761b"),
		attention:         lipgloss.Color("#b3641e"),
		busy:              lipgloss.Color("#a8761b"),
		info:              lipgloss.Color("#2a5d8f"),
		infoMoreSubtle:    lipgloss.Color("#234c74"),
		infoMostSubtle:    lipgloss.Color("#1c3d5c"),
		success:           lipgloss.Color("#3c7a45"),
		successMoreSubtle: lipgloss.Color("#326639"),
		successMostSubtle: lipgloss.Color("#28522d"),

		yolo:           lipgloss.Color("#6e4f12"),
		plan:           lipgloss.Color("#c2412a"),
		planMoreSubtle: lipgloss.Color("#d9ccb6"),

		button:         lipgloss.Color("#c2412a"),
		buttonSubtle:   lipgloss.Color("#e3daca"),
		buttonInactive: lipgloss.Color("#cfc4af"),
		buttonHovered:  lipgloss.Color("#b0a48c"),

		ansiBlack:   lipgloss.Color("#221f1a"),
		ansiRed:     lipgloss.Color("#b02a1c"),
		ansiGreen:   lipgloss.Color("#3c7a45"),
		ansiYellow:  lipgloss.Color("#8a6516"),
		ansiBlue:    lipgloss.Color("#2a5d8f"),
		ansiMagenta: lipgloss.Color("#8c3a6b"),
		ansiCyan:    lipgloss.Color("#2f5d62"),
		ansiWhite:   lipgloss.Color("#5c554a"),

		ansiBrightBlack:   lipgloss.Color("#837a6c"),
		ansiBrightRed:     lipgloss.Color("#d4442f"),
		ansiBrightGreen:   lipgloss.Color("#4f9558"),
		ansiBrightYellow:  lipgloss.Color("#a8761b"),
		ansiBrightBlue:    lipgloss.Color("#3a75ad"),
		ansiBrightMagenta: lipgloss.Color("#a94f83"),
		ansiBrightCyan:    lipgloss.Color("#3d757b"),
		ansiBrightWhite:   lipgloss.Color("#3d3830"),
	}
}

// tundraOpts returns an original light theme: cold overcast daylight, slate
// ink and steel blue, with nothing warm anywhere.
func tundraOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#2f6f8f"), // steel blue
		secondary: lipgloss.Color("#4a5fa8"), // indigo
		accent:    lipgloss.Color("#2e7d6b"), // teal
		keyword:   lipgloss.Color("#7a4a8f"), // violet

		fgBase:       lipgloss.Color("#1c2429"),
		fgMoreSubtle: lipgloss.Color("#56646d"),
		fgSubtle:     lipgloss.Color("#38454d"),
		fgMostSubtle: lipgloss.Color("#7a8790"),

		onPrimary: lipgloss.Color("#f7fafb"),

		bgBase:         lipgloss.Color("#f2f5f7"),
		bgLeastVisible: lipgloss.Color("#e6ebef"),
		bgLessVisible:  lipgloss.Color("#d7dfe5"),
		bgMostVisible:  lipgloss.Color("#bfcad3"),

		separator: lipgloss.Color("#cad8e0"),

		destructive:       lipgloss.Color("#b0453a"),
		error:             lipgloss.Color("#93362d"),
		warningSubtle:     lipgloss.Color("#6b5312"),
		warning:           lipgloss.Color("#8a6b18"),
		attention:         lipgloss.Color("#a8621c"),
		busy:              lipgloss.Color("#8a6b18"),
		info:              lipgloss.Color("#2f6f8f"),
		infoMoreSubtle:    lipgloss.Color("#285c76"),
		infoMostSubtle:    lipgloss.Color("#1f495e"),
		success:           lipgloss.Color("#2e7d6b"),
		successMoreSubtle: lipgloss.Color("#26685a"),
		successMostSubtle: lipgloss.Color("#1e5347"),

		yolo:           lipgloss.Color("#6b5312"),
		plan:           lipgloss.Color("#2f6f8f"),
		planMoreSubtle: lipgloss.Color("#cad8e0"),

		button:         lipgloss.Color("#2f6f8f"),
		buttonSubtle:   lipgloss.Color("#d7dfe5"),
		buttonInactive: lipgloss.Color("#bfcad3"),
		buttonHovered:  lipgloss.Color("#9dabb6"),

		ansiBlack:   lipgloss.Color("#1c2429"),
		ansiRed:     lipgloss.Color("#b0453a"),
		ansiGreen:   lipgloss.Color("#2e7d6b"),
		ansiYellow:  lipgloss.Color("#8a6b18"),
		ansiBlue:    lipgloss.Color("#2f6f8f"),
		ansiMagenta: lipgloss.Color("#7a4a8f"),
		ansiCyan:    lipgloss.Color("#1f6d78"),
		ansiWhite:   lipgloss.Color("#56646d"),

		ansiBrightBlack:   lipgloss.Color("#7a8790"),
		ansiBrightRed:     lipgloss.Color("#cc5546"),
		ansiBrightGreen:   lipgloss.Color("#3a9683"),
		ansiBrightYellow:  lipgloss.Color("#a8801d"),
		ansiBrightBlue:    lipgloss.Color("#3d86ab"),
		ansiBrightMagenta: lipgloss.Color("#9460ab"),
		ansiBrightCyan:    lipgloss.Color("#2a8291"),
		ansiBrightWhite:   lipgloss.Color("#38454d"),
	}
}

// sakuraOpts returns an original light theme: blossom-pink paper with plum
// ink, iris and jade held back for accents.
func sakuraOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#b23a5f"), // rose
		secondary: lipgloss.Color("#5e4a94"), // iris
		accent:    lipgloss.Color("#3f7d6b"), // jade
		keyword:   lipgloss.Color("#8a3e7a"), // magenta

		fgBase:       lipgloss.Color("#2b1f26"),
		fgMoreSubtle: lipgloss.Color("#6b5660"),
		fgSubtle:     lipgloss.Color("#4a3942"),
		fgMostSubtle: lipgloss.Color("#8b7480"),

		onPrimary: lipgloss.Color("#fdf7f7"),

		bgBase:         lipgloss.Color("#fbf4f4"),
		bgLeastVisible: lipgloss.Color("#f5e7e8"),
		bgLessVisible:  lipgloss.Color("#ebd8da"),
		bgMostVisible:  lipgloss.Color("#d6bcbf"),

		separator: lipgloss.Color("#e2cbd2"),

		destructive:       lipgloss.Color("#a83048"),
		error:             lipgloss.Color("#8a2438"),
		warningSubtle:     lipgloss.Color("#6b4b18"),
		warning:           lipgloss.Color("#8a6420"),
		attention:         lipgloss.Color("#a85a2a"),
		busy:              lipgloss.Color("#8a6420"),
		info:              lipgloss.Color("#3a5f94"),
		infoMoreSubtle:    lipgloss.Color("#30507d"),
		infoMostSubtle:    lipgloss.Color("#264166"),
		success:           lipgloss.Color("#3f7d6b"),
		successMoreSubtle: lipgloss.Color("#346858"),
		successMostSubtle: lipgloss.Color("#2a5347"),

		yolo:           lipgloss.Color("#6b4b18"),
		plan:           lipgloss.Color("#b23a5f"),
		planMoreSubtle: lipgloss.Color("#e2cbd2"),

		button:         lipgloss.Color("#b23a5f"),
		buttonSubtle:   lipgloss.Color("#ebd8da"),
		buttonInactive: lipgloss.Color("#d6bcbf"),
		buttonHovered:  lipgloss.Color("#c2a5ac"),

		ansiBlack:   lipgloss.Color("#2b1f26"),
		ansiRed:     lipgloss.Color("#a83048"),
		ansiGreen:   lipgloss.Color("#3f7d6b"),
		ansiYellow:  lipgloss.Color("#8a6420"),
		ansiBlue:    lipgloss.Color("#3a5f94"),
		ansiMagenta: lipgloss.Color("#8a3e7a"),
		ansiCyan:    lipgloss.Color("#2f6d7a"),
		ansiWhite:   lipgloss.Color("#6b5660"),

		ansiBrightBlack:   lipgloss.Color("#8b7480"),
		ansiBrightRed:     lipgloss.Color("#c94460"),
		ansiBrightGreen:   lipgloss.Color("#4f967f"),
		ansiBrightYellow:  lipgloss.Color("#a87a27"),
		ansiBrightBlue:    lipgloss.Color("#4a76ae"),
		ansiBrightMagenta: lipgloss.Color("#a3558f"),
		ansiBrightCyan:    lipgloss.Color("#3a8391"),
		ansiBrightWhite:   lipgloss.Color("#4a3942"),
	}
}

// darkOverrides picks legible ink for the badges and the H1 that quickStyle
// fills with primary. The original built-ins keep the ink they always had, but a
// theme added now has no look to preserve, so it gets text that reads.
func darkOverrides(opts func() quickStyleOpts) func(Styles) Styles {
	return func(s Styles) Styles {
		return legibleBadges(s, opts())
	}
}

// lightOverrides adapts the dark-first shared styles to a light palette. Two
// things need it: the badges filled with a bright role, which the shared code
// inks with a background role that is now also light, and the H1 and plan
// banner, which pair a warning hue with a loud primary. legibleBadges picks
// legible ink for all of them; the rest is the inline code chip, which has to
// stand out from a light message background rather than disappear into it.
func lightOverrides(opts func() quickStyleOpts) func(Styles) Styles {
	return func(s Styles) Styles {
		o := opts()
		s = legibleBadges(s, o)

		// The shared chip paints destructive on bgLessVisible. On a light
		// theme that reads, but a deeper, more saturated ink reads better
		// and keeps the chip from looking washed out.
		s.Markdown.Code.Color = hex(o.error)
		s.PlanMarkdown.Code.Color = hex(o.error)

		// Thinking blocks sit on bgLeastVisible, which is almost the page
		// color; use the quieter ink so they recede.
		s.QuietMarkdown.Document.Color = hex(o.fgSubtle)
		return s
	}
}

func abyssOverrides(s Styles) Styles    { return darkOverrides(abyssOpts)(s) }
func claudeOverrides(s Styles) Styles   { return darkOverrides(claudeOpts)(s) }
func emberOverrides(s Styles) Styles    { return darkOverrides(emberOpts)(s) }
func githubOverrides(s Styles) Styles   { return darkOverrides(githubOpts)(s) }
func nocturneOverrides(s Styles) Styles { return darkOverrides(nocturneOpts)(s) }

func papercutOverrides(s Styles) Styles { return lightOverrides(papercutOpts)(s) }
func sakuraOverrides(s Styles) Styles   { return lightOverrides(sakuraOpts)(s) }
func tundraOverrides(s Styles) Styles   { return lightOverrides(tundraOpts)(s) }
