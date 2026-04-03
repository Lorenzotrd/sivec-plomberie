package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"
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

func generateRandomName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	noun := nouns[rand.Intn(len(nouns))]
	return fmt.Sprintf("%s%s", adj, noun)
}

func main() {
	rand.Seed(time.Now().UnixNano())

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <wallet_address>")
		fmt.Println("Example: go run main.go 0x1234567890abcdef...")
		os.Exit(1)
	}

	address := os.Args[1]
	name := generateRandomName()

	fmt.Println("=========================================")
	fmt.Printf("Address: %s\n", address)
	fmt.Printf("Generated Username: %s\n", name)
	fmt.Println("=========================================")
}
