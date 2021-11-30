package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bearbin/mcgorcon"
	"github.com/bwmarrin/discordgo"
	"github.com/lus/dgc"
)

type Config struct {
	BotToken      string          `json:"bot_token"`
	MinecraftInfo MinecraftServer `json:"minecraft"`
}

type MinecraftServer struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
}

var (
	HermesConfig  Config
	WarningLogger *log.Logger
	InfoLogger    *log.Logger
	ErrorLogger   *log.Logger
	MCClient      mcgorcon.Client
)

func init() {
	configData, err := ioutil.ReadFile("cerberus.config")
	if err != nil {
		configData = []byte(os.Getenv("cerberus_config"))
	}

	file, err := os.OpenFile("logs.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Fatal(err)
	}
	InfoLogger = log.New(file, "[ INFO ] [CERBERUS]: ", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLogger = log.New(file, "[WARNING] [CERBERUS]: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(file, "[ ERROR ] [CERBERUS]: ", log.Ldate|log.Ltime|log.Lshortfile)

	//& gets the variables memory address
	json.Unmarshal(configData, &HermesConfig)

	MCClient, err = mcgorcon.Dial(string(HermesConfig.MinecraftInfo.Host), int(HermesConfig.MinecraftInfo.Port), string(HermesConfig.MinecraftInfo.Password))
	if err != nil {
		ErrorLogger.Fatalln("Could not connect to server. Is it up?", err)
	}

}

func main() {
	router := dgc.Create(&dgc.Router{
		// We will allow '!' and 'example!' as the bot prefixes
		Prefixes: []string{
			"c+",
		},

		// We will ignore the prefix case, so 'eXaMpLe!' is also a valid prefix
		IgnorePrefixCase: true,

		// We don't want bots to be able to execute our commands
		BotsAllowed: false,

		// We may initialize our commands in here, but we will use the corresponding method later on
		Commands: []*dgc.Command{},

		// This handler gets called if the bot just got pinged (no argument provided)
		PingHandler: func(ctx *dgc.Ctx) {
			ctx.RespondText("Pong!")
		},
	})

	// Create a new Discord session using the provided bot token.
	session, err := discordgo.New("Bot " + HermesConfig.BotToken)
	if err != nil {
		log.Fatal("error creating Discord session,", err)
		return
	}

	// Open a websocket connection to Discord and begin listening.
	err = session.Open()
	if err != nil {
		log.Fatal("error opening connection,", err)
		return
	}

	router.RegisterDefaultHelpCommand(session, nil)

	// Register the messageCreate func as a callback for MessageCreate events.
	//session.AddHandler(messageCreate)

	// In this example, we only care about receiving message events.
	session.Identify.Intents = discordgo.IntentsGuildMessages

	// Register a simple command that responds with our custom object
	router.RegisterCmd(&dgc.Command{
		// We want to use 'obj' as the primary name of the command
		Name: "whitelist",

		// We also want the command to get triggered with the 'object' alias
		Aliases: []string{
			"wl",
		},

		// These fields get displayed in the default help messages
		Description: "Whitelists the specified minecraft user to the server.",
		Usage:       "whitelist <option> [username]",
		Example:     "whitelist add Steve",

		// You can assign custom flags to a command to use them in middlewares
		Flags: []string{},

		// We want to ignore the command case
		IgnoreCase: true,

		// You may define sub commands in here
		SubCommands: []*dgc.Command{},

		// We want the user to be able to execute this command once in five seconds and the cleanup interval shpuld be one second
		RateLimiter: dgc.NewRateLimiter(5*time.Second, 1*time.Second, func(ctx *dgc.Ctx) {
			ctx.RespondText("You are being rate limited!")
		}),

		// Now we want to define the command handler
		Handler: messageCreate,
	})

	router.RegisterCmd(&dgc.Command{
		Name:        "reconnect",
		Description: "Attempts to reconnect to the Minecraft Server",
		Usage:       "reconnect",
		Example:     "reconnect",
		IgnoreCase:  true,
		RateLimiter: dgc.NewRateLimiter(5*time.Second, 1*time.Second, func(ctx *dgc.Ctx) {
			ctx.RespondText("You are being rate limited!")
		}),
		Handler: retryConnection,
	})

	router.Initialize(session)

	// Wait here until CTRL-C or other term signal is received.
	InfoLogger.Println("Bot is now running.")
	//Do http stuff
	handler := http.HandlerFunc(handleRequest)
	http.Handle("/", handler)
	http.ListenAndServe(":"+os.Getenv("PORT"), nil)
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close down the Discord session.
	InfoLogger.Println("Received SIGTERM - shutting down")
	session.Close()
}

// This function will be called (due to AddHandler above) every time a new
// message is created on any channel that the authenticated bot has access to.
func messageCreate(ctx *dgc.Ctx) {
	arguments := ctx.Arguments
	switch arguments.Get(0).Raw() {
	case "add":
		username := arguments.Get(1).Raw()
		InfoLogger.Println("Add User " + username + " requested by " + ctx.Event.Author.Username + "#" + ctx.Event.Author.Discriminator)
		response := addUserToWhitelist(username, MCClient)
		if !response {
			ctx.Session.ChannelMessageSendReply(ctx.Event.ChannelID, "Could not whitelist "+username+". Server appears down.", ctx.Event.Message.Reference())
		} else {
			ctx.Session.ChannelMessageSendReply(ctx.Event.ChannelID, "Whitelisted "+username+".", ctx.Event.Message.Reference())
		}
	case "remove":
		fallthrough
	case "rm":
		username := arguments.Get(1).Raw()
		member, err := ctx.Session.GuildMember(ctx.Event.GuildID, ctx.Event.Author.ID)
		if err != nil {
			ErrorLogger.Println(err)
		}
		if !contains("841184009802219520", member.Roles) {
			ctx.Session.ChannelMessageSendReply(ctx.Event.ChannelID, "You are not allowed to remove "+username+" from the whitelist.", ctx.Event.Message.Reference())
			return
		}
		InfoLogger.Println("Remove User " + username + " requested by " + ctx.Event.Author.Username + "#" + ctx.Event.Author.Discriminator)
		response := removeUserFromWhitelist(username, MCClient)
		if !response {
			ctx.Session.ChannelMessageSendReply(ctx.Event.ChannelID, "Could not remove "+username+" from whitelist. Server appears down.", ctx.Event.Message.Reference())
		} else {
			ctx.Session.ChannelMessageSendReply(ctx.Event.ChannelID, "Removed "+username+" from whitelist.", ctx.Event.Message.Reference())
		}
	case "ls":
		fallthrough
	case "list":
		InfoLogger.Println("List users requested by " + ctx.Event.Author.Username + "#" + ctx.Event.Author.Discriminator)
		response := listUsersInWhitelist(MCClient)
		ctx.Session.ChannelMessageSendReply(ctx.Event.ChannelID, response, ctx.Event.Message.Reference())
	default:
		WarningLogger.Println("invalid command", arguments.Raw())
		ctx.Session.ChannelMessageSendReply(ctx.Event.ChannelID, "I don't recognize that command!", ctx.Event.Message.Reference())
	}
}

func addUserToWhitelist(username string, client mcgorcon.Client) bool {
	response, err := client.SendCommand("whitelist add " + username)
	if err != nil {
		ErrorLogger.Println("Could not add user " + username)
		return false
	} else {
		client.SendCommand("whitelist reload")
		InfoLogger.Println("Response: " + response)
		return true
	}
}

func removeUserFromWhitelist(username string, client mcgorcon.Client) bool {
	response, err := client.SendCommand("whitelist remove " + username)
	if err != nil {
		ErrorLogger.Println("Could not remove user " + username)
		return false
	} else {
		client.SendCommand("whitelist reload")
		InfoLogger.Println("Response: " + response)
		return true
	}
}

func listUsersInWhitelist(client mcgorcon.Client) string {
	response, err := client.SendCommand("whitelist list")
	if err != nil {
		ErrorLogger.Println("Could not list users")
		return "Error: could not list users."
	} else {
		return response
	}
}

func retryConnection(ctx *dgc.Ctx) {
	client, err := mcgorcon.Dial(string(HermesConfig.MinecraftInfo.Host), int(HermesConfig.MinecraftInfo.Port), string(HermesConfig.MinecraftInfo.Password))
	if err != nil {
		ErrorLogger.Println("Could not connect ", err)
		ctx.Session.ChannelMessageSendReply(ctx.Event.ChannelID, "Could not reconnect to server.", ctx.Event.Message.Reference())
	} else {
		MCClient = client
		WarningLogger.Println("Reconnected to server!")
	}
}

func contains(value string, array []string) bool {
	for _, element := range array {
		if element == value {
			return true
		}
	}
	return false
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	resp := make(map[string]string)
	resp["message"] = "Status OK"
	jsonResp, err := json.Marshal(resp)
	if err != nil {
		log.Fatalf("Error happened in JSON marshal. Err: %s", err)
	}
	w.Write(jsonResp)
	return
}
