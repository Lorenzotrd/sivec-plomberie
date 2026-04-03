package cli

import (
	"fmt"
	"os"
	"runtime"
)

// ANSI color codes
const (
	Reset = "\033[0m"
	Bold  = "\033[1m"
	Dim   = "\033[2m"

	// Regular colors
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"

	// Bright colors
	BrightRed     = "\033[91m"
	BrightGreen   = "\033[92m"
	BrightYellow  = "\033[93m"
	BrightBlue    = "\033[94m"
	BrightMagenta = "\033[95m"
	BrightCyan    = "\033[96m"
	BrightWhite   = "\033[97m"

	// Background colors
	BgRed     = "\033[41m"
	BgGreen   = "\033[42m"
	BgYellow  = "\033[43m"
	BgBlue    = "\033[44m"
	BgMagenta = "\033[45m"
	BgCyan    = "\033[46m"
)

var colorsEnabled = true

func init() {
	// Disable colors on Windows (unless using Windows Terminal or similar)
	if runtime.GOOS == "windows" {
		// Check for modern Windows terminal support
		if os.Getenv("WT_SESSION") == "" && os.Getenv("TERM_PROGRAM") == "" {
			colorsEnabled = false
		}
	}
	// Disable colors if NO_COLOR env is set
	if os.Getenv("NO_COLOR") != "" {
		colorsEnabled = false
	}
}

// DisableColors disables color output
func DisableColors() {
	colorsEnabled = false
}

// EnableColors enables color output
func EnableColors() {
	colorsEnabled = true
}

func colorize(color, text string) string {
	if !colorsEnabled {
		return text
	}
	return color + text + Reset
}

// Color functions
func RedText(text string) string     { return colorize(Red, text) }
func GreenText(text string) string   { return colorize(Green, text) }
func YellowText(text string) string  { return colorize(Yellow, text) }
func BlueText(text string) string    { return colorize(Blue, text) }
func MagentaText(text string) string { return colorize(Magenta, text) }
func CyanText(text string) string    { return colorize(Cyan, text) }
func WhiteText(text string) string   { return colorize(White, text) }
func BoldText(text string) string    { return colorize(Bold, text) }
func DimText(text string) string     { return colorize(Dim, text) }

func BrightRedText(text string) string     { return colorize(BrightRed, text) }
func BrightGreenText(text string) string   { return colorize(BrightGreen, text) }
func BrightYellowText(text string) string  { return colorize(BrightYellow, text) }
func BrightBlueText(text string) string    { return colorize(BrightBlue, text) }
func BrightMagentaText(text string) string { return colorize(BrightMagenta, text) }
func BrightCyanText(text string) string    { return colorize(BrightCyan, text) }

// Semantic color functions for bet-bot
func Success(text string) string   { return colorize(BrightGreen, text) }
func Error(text string) string     { return colorize(BrightRed, text) }
func Warning(text string) string   { return colorize(BrightYellow, text) }
func Info(text string) string      { return colorize(BrightCyan, text) }
func Highlight(text string) string { return colorize(BrightMagenta, text) }

// BotPrefix returns a colored [bot-name] prefix for manager output.
func BotPrefix(name string) string {
	var color string
	switch name {
	case "bet-bot":
		color = BrightGreen
	case "candle-rush":
		color = BrightMagenta
	case "sweep":
		color = BrightYellow
	case "manager":
		color = BrightCyan
	default:
		color = White
	}
	return colorize(color+Bold, "["+name+"]")
}

// Amount formats a USDC amount with color
func Amount(amount float64) string {
	return colorize(BrightYellow+Bold, fmt.Sprintf("%.2f USDC", amount))
}

// AmountMON formats a MON amount with color
func AmountMON(amount float64) string {
	return colorize(BrightCyan, fmt.Sprintf("%.4f MON", amount))
}

// Address formats an address (shortened) with color
func Address(addr string) string {
	short := addr
	if len(addr) > 10 {
		short = addr[:10] + "..."
	}
	return colorize(Cyan, short)
}

// TxHash formats a transaction hash with color
func TxHash(hash string) string {
	return colorize(Blue, hash)
}

// Market formats a market description with color
func Market(market string) string {
	return colorize(BrightMagenta+Bold, market)
}

// Direction formats UP/DOWN direction with color
func Direction(dir string) string {
	switch dir {
	case "UP", "FIRST":
		return colorize(BrightGreen+Bold, dir)
	case "DOWN", "SECOND":
		return colorize(BrightRed+Bold, dir)
	default:
		return colorize(Yellow, dir)
	}
}

// BetType formats bet type with color
func BetType(betType string) string {
	switch betType {
	case "UP_DOWN":
		return colorize(BrightBlue+Bold, betType)
	case "RELATIVE":
		return colorize(BrightMagenta+Bold, betType)
	default:
		return colorize(White, betType)
	}
}

// Timeframe formats a timeframe with color
func Timeframe(tf string) string {
	return colorize(Cyan, tf)
}

// Status formats status messages
func StatusOK(text string) string {
	if !colorsEnabled {
		return "[OK] " + text
	}
	return colorize(BrightGreen, "[OK]") + " " + text
}

func StatusFail(text string) string {
	if !colorsEnabled {
		return "[FAIL] " + text
	}
	return colorize(BrightRed, "[FAIL]") + " " + text
}

func StatusWarn(text string) string {
	if !colorsEnabled {
		return "[WARN] " + text
	}
	return colorize(BrightYellow, "[WARN]") + " " + text
}

func StatusInfo(text string) string {
	if !colorsEnabled {
		return "[INFO] " + text
	}
	return colorize(BrightCyan, "[INFO]") + " " + text
}

// Separator returns a dim separator line
func Separator() string {
	return colorize(Dim, "────────────────────────────────────────────────────────────")
}

// SeparatorShort returns a shorter separator
func SeparatorShort() string {
	return colorize(Dim, "────────────────────────────")
}

// Banner prints a colored banner
func Banner(text string) string {
	if !colorsEnabled {
		return "\n=== " + text + " ===\n"
	}
	return "\n" + colorize(BrightCyan+Bold, "═══ "+text+" ═══") + "\n"
}

// CycleHeader formats a betting cycle header
func CycleHeader(cycleNum int) string {
	header := fmt.Sprintf("BETTING CYCLE #%d", cycleNum)
	if !colorsEnabled {
		return "\n--- " + header + " ---"
	}
	return "\n" + colorize(Cyan, "┌─ "+header+" ─────────────────────────────────────┐")
}

// CycleFooter formats a betting cycle footer
func CycleFooter() string {
	if !colorsEnabled {
		return "---"
	}
	return colorize(Dim, "└──────────────────────────────────────────────────────────┘")
}

// BetPlaced formats the bet placed success message
func BetPlaced(betType, market, direction, timeframe string, amount float64, txHash string) string {
	box := colorize(Green, "│")
	checkmark := colorize(BrightGreen+Bold, "✓")

	return fmt.Sprintf(`
%s
%s  %s %s
%s
%s  %s  %s  %s  %s
%s  %s  %s
%s
%s  %s %s
%s`,
		colorize(Green, "┌────────────────────────────────────────────────────────────┐"),
		box, checkmark, colorize(BrightGreen+Bold, "BET PLACED SUCCESSFULLY"),
		colorize(Green, "├────────────────────────────────────────────────────────────┤"),
		box, BetType(betType), Market(market), Direction(direction), Timeframe(timeframe),
		box, DimText("Amount:"), Amount(amount),
		colorize(Green, "├────────────────────────────────────────────────────────────┤"),
		box, DimText("TX:"), TxHash(txHash),
		colorize(Green, "└────────────────────────────────────────────────────────────┘"),
	)
}

// WalletInfo formats wallet information in a compact way
func WalletInfo(addr string, isNew bool, monBalance, usdcBalance float64) string {
	status := DimText("existing")
	if isNew {
		status = Success("NEW")
	}
	return fmt.Sprintf("%s %s  %s  %s",
		DimText("Wallet:"),
		Address(addr),
		status,
		DimText(fmt.Sprintf("[%s | %s]",
			fmt.Sprintf("%.4f MON", monBalance),
			fmt.Sprintf("%.2f USDC", usdcBalance))),
	)
}

// MarketInfo formats market selection info
func MarketInfo(betType, market, direction, timeframe string, amount float64) string {
	return fmt.Sprintf("%s %s %s %s %s %s",
		DimText("Bet:"),
		BetType(betType),
		Market(market),
		Direction(direction),
		Timeframe("("+timeframe+")"),
		Amount(amount),
	)
}
