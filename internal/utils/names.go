package utils

import (
	"fmt"
	"math/rand"
	"strings"
)

var (
	adjectives = []string{
		"Cyber", "Neon", "Quantum", "Sonic", "Mega", "Hyper", "Ultra",
		"Swift", "Rapid", "Turbo", "Flash", "Zero", "Alpha", "Omega",
		"Brave", "Calm", "Cool", "Eager", "Fair", "Grand", "Happy",
		"Jolly", "Kind", "Lively", "Mighty", "Noble", "Proud", "Quick",
		"Royal", "Sharp", "Smart", "Strong", "Super", "Sweet", "Tough",
		"Wise", "Wild", "Young", "Zesty", "Vivid", "Lucky", "Magic",
	}

	nouns = []string{
		// Anime/Pop Culture
		"Goku", "Luffy", "Naruto", "Saitama", "Deku", "Asta", "Tanjiro",
		"Eren", "Levi", "Zoro", "Sanji", "Nami", "Kira", "L", "Rem",
		"Asuna", "Kirito", "Gojo", "Itadori", "Sukuna", "Denji", "Power",

		// Animals
		"Tiger", "Lion", "Wolf", "Bear", "Eagle", "Hawk", "Fox", "Cat",
		"Dog", "Panda", "Koala", "Shark", "Whale", "Dolphin", "Snake",
		"Dragon", "Phoenix", "Griffin", "Hydra", "Titan", "Giant",

		// Tech/Sci-Fi
		"Droid", "Mech", "Bot", "Cyborg", "Clone", "Drone", "Pilot",
		"Racer", "Walker", "Runner", "Surfer", "Hunter", "Warrior",
		"Ninja", "Samurai", "Knight", "Wizard", "Mage", "Sage", "Hero",
	}
)

// GenerateRandomName generates a random "AdjectiveNoun" style name
func GenerateRandomName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	noun := nouns[rand.Intn(len(nouns))]
	return fmt.Sprintf("%s%s", adj, noun)
}

// GenerateUniqueReferralCode generates a valid referral code
// Format: 4-20 uppercase letters only (no numbers, no special chars)
// e.g. "NEONTIGER", "CYBERGOKU", "MEGAWOLF"
func GenerateUniqueReferralCode() string {
	name := GenerateRandomName()
	// Remove any non-letter characters and convert to uppercase
	code := strings.ToUpper(name)

	// Ensure minimum length of 4
	if len(code) < 4 {
		code = code + "CODE"
	}

	// Truncate if over 20
	if len(code) > 20 {
		code = code[:20]
	}

	return code
}
