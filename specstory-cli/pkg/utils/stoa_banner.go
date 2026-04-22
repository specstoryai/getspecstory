package utils

import (
	"fmt"
	"sync"
)

// ANSI 256-color code closest to Stoa's brand indigo (#4F46E5)
const (
	stoaBlue  = "\033[38;5;63m"
	stoaReset = "\033[0m"
)

var stoaBannerOnce sync.Once

// ShowStoaBanner displays the Stoa advertisement banner exactly once per CLI invocation.
// Safe to call multiple times from different code paths; only the first call prints.
//
//	func ShowStoaBanner() {
//		stoaBannerOnce.Do(func() {
//			b := stoaBlue
//			r := stoaReset
//			fmt.Println()
//			fmt.Printf("%s╭────────────────────────────────────────────────────────╮\n", b)
//			fmt.Printf("%s|  @%s             %s@%s                                       %s|\n", b, r, b, r, b)
//			fmt.Printf("%s|  @@@%s         %s@@@%s    Stoa — Where makers meet           %s|\n", b, r, b, r, b)
//			fmt.Printf("%s|    @@%s       %s@@%s                                         %s|\n", b, r, b, r, b)
//			fmt.Printf("%s|      @@@@@@@%s         Multiplayer AI for product teams. %s|\n", b, r, b)
//			fmt.Printf("%s|       @@@@@%s          Live conversations become         %s|\n", b, r, b)
//			fmt.Printf("%s|      @@@@@@@%s         code, decisions & plans.          %s|\n", b, r, b)
//			fmt.Printf("%s|    @@%s       %s@@%s                                         %s|\n", b, r, b, r, b)
//			fmt.Printf("%s|  @@@%s         %s@@@%s     Start free → withstoa.com         %s|\n", b, r, b, r, b)
//			fmt.Printf("%s|  @%s             %s@%s                                       %s|\n", b, r, b, r, b)
//			fmt.Printf("%s╰────────────────────────────────────────────────────────╯%s\n", b, r)
//			fmt.Println()
//		})
//	}
// func ShowStoaBanner() {
// 	stoaBannerOnce.Do(func() {
// 		b := stoaBlue
// 		r := stoaReset
// 		fmt.Println()
// 		fmt.Printf("%s╭──────────────────────────────────────────────────────────╮\n", b)
// 		fmt.Printf("%s|                                                          |\n", b)
// 		fmt.Printf("%s|   \\     /         %sStoa — Where makers meet               %s|\n", b, r, b)
// 		fmt.Printf("%s|    \\   /                                                 |\n", b)
// 		fmt.Printf("%s|     ╭─╮         %sMultiplayer AI for product teams.        %s|\n", b, r, b)
// 		fmt.Printf("%s|     ╰─╯      %sLive conversations become code & decisions. %s|\n", b, r, b)
// 		fmt.Printf("%s|    /   \\                                                 |\n", b)
// 		fmt.Printf("%s|   /     \\         %sStart free → withstoa.com              %s|\n", b, r, b)
// 		fmt.Printf("%s|                                                          |\n", b)
// 		fmt.Printf("%s╰──────────────────────────────────────────────────────────╯%s\n", b, r)
// 		fmt.Println()
// 	})
// }

func ShowStoaBanner(silent bool) {
	if silent {
		return
	}
	stoaBannerOnce.Do(func() {
		b := stoaBlue
		r := stoaReset
		fmt.Println()
		fmt.Printf("%s╭───────────────────────────────────────────────────╮\n", b)
		fmt.Printf("%s|                                                   |\n", b)
		fmt.Printf("%s|   \\       /       %sStoa — Where makers meet        %s|\n", b, r, b)
		fmt.Printf("%s|    \\     /                                        |\n", b)
		fmt.Printf("%s|     ╭───╮       %sMultiplayer AI for product teams. %s|\n", b, r, b)
		fmt.Printf("%s|     |   |          %sLive conversations become      %s|\n", b, r, b)
		fmt.Printf("%s|     ╰───╯           %scode, decisions & plans.      %s|\n", b, r, b)
		fmt.Printf("%s|    /     \\                                        |\n", b)
		fmt.Printf("%s|   /       \\       %sStart free → %swithstoa.com       %s|\n", b, r, b, b)
		fmt.Printf("%s|                                                   |\n", b)
		fmt.Printf("%s╰───────────────────────────────────────────────────╯%s\n", b, r)
		fmt.Println()
	})
}

// \      /
//  \    /
//   ╭-╮
// 	╰-╯
//  /    \
// /      \
