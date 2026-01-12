package utils

import (
	_ "embed"
	"math/rand"
)

// Logo data embedded from files (256-color versions for Terminal.app compatibility)
// See .specstory/history/2025-08-06_18-51-59Z-bash-input-npx-oh.md and .specstory/history/2025-08-06_19-26-59Z-this-session-is-being.md
// for the history of how these logos were created and how to regenerate them, ensuring they are 256 color versions, not 24-bit color versions

//go:embed logos/logo_grad_blue.txt
var logoGradBlue string

//go:embed logos/logo_sunset.txt
var logoSunset string

//go:embed logos/logo_dawn.txt
var logoDawn string

//go:embed logos/logo_nebula.txt
var logoNebula string

//go:embed logos/logo_ocean.txt
var logoOcean string

//go:embed logos/logo_fire.txt
var logoFire string

//go:embed logos/logo_forest.txt
var logoForest string

//go:embed logos/logo_gold.txt
var logoGold string

//go:embed logos/logo_purple.txt
var logoPurple string

//go:embed logos/logo_mint.txt
var logoMint string

//go:embed logos/logo_coral.txt
var logoCoral string

//go:embed logos/logo_matrix.txt
var logoMatrix string

// ColoredLogos contains all the available logo color palettes
var ColoredLogos []string

// GetRandomLogo returns a randomly selected colored logo
func GetRandomLogo() string {
	return ColoredLogos[rand.Intn(len(ColoredLogos))]
}

func init() {
	// Initialize logos array
	ColoredLogos = []string{
		logoGradBlue,
		logoSunset,
		logoDawn,
		logoNebula,
		logoOcean,
		logoFire,
		logoForest,
		logoGold,
		logoPurple,
		logoMint,
		logoCoral,
		logoMatrix,
	}
}
