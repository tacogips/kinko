package kinko

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

var mnemonicWords = []string{
	"able", "about", "acid", "adapt", "agent", "angle", "apple", "armor",
	"basic", "beach", "blend", "brave", "breeze", "bright", "cable", "calm",
	"carry", "cedar", "chase", "civic", "clean", "cloud", "cobalt", "craft",
	"dance", "delta", "drift", "dune", "eager", "echo", "ember", "fable",
	"fancy", "field", "flame", "focus", "frost", "glide", "grain", "graph",
	"harbor", "honey", "index", "ivory", "jolly", "knock", "laser", "lemon",
	"light", "lunar", "magic", "maple", "matrix", "merit", "mild", "mint",
	"noble", "oasis", "ocean", "olive", "orbit", "panel", "pearl", "pilot",
}

func generateMnemonic(wordCount int) (string, error) {
	if wordCount <= 0 {
		return "", fmt.Errorf("invalid mnemonic size")
	}
	parts := make([]string, wordCount)
	max := big.NewInt(int64(len(mnemonicWords)))
	for i := 0; i < wordCount; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		parts[i] = mnemonicWords[n.Int64()]
	}
	return strings.Join(parts, " "), nil
}
