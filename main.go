package main

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	_ "modernc.org/sqlite" // SQLite Driver
)

// Global database connection
var db *sql.DB

func main() {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Error loading .env file:", err)
	}

	// Connect to SQLite
	db, err = sql.Open("sqlite", "./leaderboard.db")
	if err != nil {
		fmt.Println("Error connecting to database:", err)
		return
	}
	defer db.Close()

	// Create a database table if it doesn't already exist
	initializeDatabase()

	// Get bot token from environment
	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	if botToken == "" {
		fmt.Println("Bot token not set!")
		return
	}

	// Create a new Discord session
	dg, err := discordgo.New("Bot " + botToken)
	if err != nil {
		fmt.Println("Error creating Discord session:", err)
		return
	}

	// Register message handler
	dg.AddHandler(onMessageCreate)

	// Open the bot connection
	err = dg.Open()
	if err != nil {
		fmt.Println("Error opening connection:", err)
		return
	}
	defer dg.Close()

	fmt.Println("Bot is running. Press CTRL+C to exit.")
	select {} // Keep the bot running until interrupted
}

// Create the database table
func initializeDatabase() {
	createTableSQL := `
    CREATE TABLE IF NOT EXISTS leaderboard (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        username TEXT NOT NULL UNIQUE,
        score INTEGER NOT NULL,
		days_played INTEGER NOT NULL DEFAULT 0
    );`
	_, err := db.Exec(createTableSQL)
	if err != nil {
		fmt.Println("Error creating table:", err)
	}
}

// Handle received messages
func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore the bot's own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Command to display all-time leaderboard
	if strings.HasPrefix(strings.ToLower(m.Content), "!leaderboard") {
		sendLeaderboard(s, m.ChannelID)
	}

	// Debug: Log the received message
	fmt.Printf("Message received from %s: %s\n", m.Author.Username, m.Content)

	// Check if the sender is "Wordle#2092"
	if m.Author.Username == "Wordle" && m.Author.Discriminator == "2092" {
		// Additional check: Look for "results" in the content
		if strings.Contains(strings.ToLower(m.Content), "results") {
			fmt.Printf("Processing results message from Wordle#2092: %s\n", m.Content)
			processWordleResultsMessage(m.Content, s, m.ChannelID)
		}
	} else {
		fmt.Println("Message ignored. Not from Wordle #2092.")
	}

	// if strings.Contains(strings.ToLower(m.Content), "results") {
	// 	fmt.Printf("Processing results message from Wordle#2092: %s\n", m.Content)
	// 	processWordleResultsMessage(m.Content, s, m.ChannelID)
	// }
}

// Parse Wordle messages and update the database
func processWordleResultsMessage(message string, s *discordgo.Session, channelID string) {
	// Split the message into lines by newline
	lines := strings.Split(message, "\n")

	// Regex patterns for scores and usernames
	scoreRegex := regexp.MustCompile(`(\d+)/6|X/6`) // Matches "1/6", "2/6", etc.
	userRegex := regexp.MustCompile(`@\S+`)         // Matches "@username"

	// Track all users in the daily results
	dailyUsers := make(map[string]int) // username -> score

	// Parse the message
	for _, line := range lines {
		// Check if the line contains a score match
		scoreMatch := scoreRegex.FindString(line)
		if scoreMatch != "" {
			score := 0
			// Extract the numeric score
			if strings.HasPrefix(scoreMatch, "X") {
				score = 7 // X/6 gets 7 penalty points
			} else {
				score, _ = strconv.Atoi(strings.Split(scoreMatch, "/")[0]) // e.g., "3/6" -> 3
			}

			// Extract usernames from the line
			usernames := userRegex.FindAllString(line, -1)
			for _, user := range usernames {
				user = cleanUsername(user) // Normalize the username
				dailyUsers[user] = score   // Add user to the daily user map
			}
		}
	}

	// Debug: Log daily users
	fmt.Println("Daily Wordle results:", dailyUsers)

	// Update scores in the database
	updateScoresBasedOnResults(dailyUsers)

	// Send acknowledgment that results were processed
	s.ChannelMessageSend(channelID, "Daily results successfully processed!")
	sendLeaderboard(s, channelID)
}

// Helper method to clean and format usernames
func cleanUsername(username string) string {
	username = strings.TrimSpace(username)
	username = strings.Trim(username, "@<>") // Remove leading "@" if present
	return username
}

func updateScoresBasedOnResults(dailyUsers map[string]int) {
	// Get all users already in the database
	rows, err := db.Query("SELECT username FROM leaderboard")
	if err != nil {
		fmt.Println("Error querying database for users:", err)
		return
	}
	defer rows.Close()

	// Build a set of all users in the database
	dbUsers := make(map[string]bool)
	for rows.Next() {
		var username string
		err := rows.Scan(&username)
		if err != nil {
			fmt.Println("Error scanning database row:", err)
			continue
		}
		dbUsers[username] = true // Mark the user as existing in the database
	}

	// Process the daily results (update cumulative scores and mark processed users)
	for user, score := range dailyUsers {
		updateCumulativeScore(user, score, true) // Mark as a scored day
		dbUsers[user] = false                    // Mark this user as "processed" (present in results)
	}

	// Add 7-point penalties for users not in daily results
	for user := range dbUsers {
		if dbUsers[user] && user != "Dumb Ass Nigga" { // Skip excluded users
			fmt.Printf("Adding penalty for %s (absent in daily results)\n", user)
			updateCumulativeScore(user, 7, false) // Penalty without incrementing days
		}
	}
}

func updateCumulativeScore(username string, score int, incrementDays bool) {
	var currentScore, daysPlayed int

	// Check if the user already exists in the database
	err := db.QueryRow("SELECT score, days_played FROM leaderboard WHERE username = ?", username).Scan(&currentScore, &daysPlayed)
	if err == sql.ErrNoRows {
		// If the user doesn't exist, insert them with their current score and 1 day played
		newDaysPlayed := 0
		if incrementDays {
			newDaysPlayed = 1
		}
		_, err := db.Exec("INSERT INTO leaderboard (username, score, days_played) VALUES (?, ?, ?)", username, score, newDaysPlayed)
		if err != nil {
			fmt.Println("Error inserting new user:", err)
		}
	} else if err == nil {
		// If the user exists, update their total score
		newTotal := currentScore + score
		newDaysPlayed := daysPlayed
		if incrementDays {
			newDaysPlayed += 1
		}
		_, err := db.Exec("UPDATE leaderboard SET score = ?, days_played = ? WHERE username = ?", newTotal, newDaysPlayed, username)
		if err != nil {
			fmt.Println("Error updating user score and days played:", err)
		}
	} else {
		fmt.Println("Error querying user:", err)
	}
}

// Fetch and send the leaderboard
func sendLeaderboard(s *discordgo.Session, channelID string) {
	// Query leaderboard data
	rows, err := db.Query("SELECT username, score, days_played FROM leaderboard WHERE days_played > 0 ORDER BY (score * 1.0 / days_played) ASC, days_played DESC, username ASC")
	if err != nil {
		fmt.Println("Error fetching leaderboard:", err)
		return
	}
	defer rows.Close()

	output := "📊 **Wordle Leaderboard (Average Score)** 📊\n"
	rank := 1

	for rows.Next() {
		var username string
		var totalScore, daysPlayed int
		err := rows.Scan(&username, &totalScore, &daysPlayed)
		if err != nil {
			fmt.Println("Error scanning leaderboard row:", err)
			continue
		}

		// Calculate the average score
		averageScore := float64(totalScore) / float64(daysPlayed)

		// Medals for top 3
		var medal string
		switch rank {
		case 1:
			medal = "🥇"
		case 2:
			medal = "🥈"
		case 3:
			medal = "🥉"
		default:
			medal = fmt.Sprintf("%d.", rank)
		}

		// Format the leaderboard entry
		output += fmt.Sprintf("%s <@%s> - %.2f\n", medal, username, averageScore)
		rank++
	}

	// If no rows are found, notify the channel
	if rank == 1 {
		output += "No results available yet!"
	}

	// Send the message to the Discord channel
	_, err = s.ChannelMessageSend(channelID, output)
	if err != nil {
		fmt.Println("Error sending leaderboard:", err)
	}
}
