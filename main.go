package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/openai/openai-go"
	"github.com/scrapli/scrapligo/driver/options"
	"github.com/scrapli/scrapligo/platform"
)

var testMetaData = `
{"router_type": "Cisco XRv 9000", "Virtual": true}
`

var historyBuffer bytes.Buffer

type payload struct {
	Question string `json:"question"`
	Metadata string `json:"metadata"`
	Previous string `json:"previous_content"`
}

func main() {
	question := flag.String("question", "", "question")
	host := flag.String("host", "", "host")
	flag.Parse()

	for {
		p := payload{
			Question: *question,
			Metadata: testMetaData,
		}
		p.Previous = string(historyBuffer.Bytes())
		var buffer bytes.Buffer

		payloadBytes, err := json.Marshal(p)
		if err != nil {
			panic(err)
		}
		client := openai.NewClient()
		chatCompletion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
			Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage("You are a network engineer that troubleshoots networking issues. You only" +
					"ever will return commands with the problem that can be ran and you WILL NEVER, I REPEAT, NEVER return" +
					"any command that will alter the configuration, any debug commands, or any other command known to cause issues" +
					"such as ping etc that will cause harm or cause the system to hang. Only give commands that are one command per line." +
					" It is also very important that if you can not determine the issue from the context that is passed in you will " +
					"give commands to run until you have a high certainty that you know the exact issue that is data driven and " +
					"not just theory. If you know what the issue is don't go any further and you need to say the stop phrase " +
					"VIVACISCO and then give a detail explanation of the problem and how one would resolve it. The output should be in markdown."),
				openai.UserMessage(string(payloadBytes)),
			}),
			Model: openai.F(openai.ChatModelGPT4o),
		})
		if err != nil {
			panic(err.Error())
		}
		if strings.Contains(chatCompletion.Choices[0].Message.Content, "VIVACISCO") {
			clearScreen()
			explanation := strings.Split(chatCompletion.Choices[0].Message.Content, "VIVACISCO")[1]
			out, err := glamour.Render("# AI TAC EXPLANATION\n"+explanation, "dark")
			if err != nil {
				panic(err.Error())
			}
			fmt.Println(out)
			os.Exit(0)
		}
		historyBuffer.WriteString(chatCompletion.Choices[0].Message.Content)
		commandsToRun := strings.Split(chatCompletion.Choices[0].Message.Content, "\n")

		// Scrapli Setup
		pe, err := platform.NewPlatform(
			"cisco_iosxr",
			*host,
			options.WithAuthNoStrictKey(),
			options.WithAuthUsername("clab"),
			options.WithAuthPassword("clab@123"),
		)
		if err != nil {
			fmt.Printf("failed to create platform; error: %+v\n", err)
			return
		}
		d, err := pe.GetNetworkDriver()
		if err != nil {
			fmt.Printf("failed to fetch network driver from the platform; error: %+v\n", err)
			return
		}
		err = d.Open()
		if err != nil {
			fmt.Printf("failed to open driver; error: %+v\n", err)
		}
		defer d.Close()
		results, err := d.SendCommands(commandsToRun)
		if err != nil {
			fmt.Printf("failed to send commands; error: %+v\n", err)
		}
		for i := range results.Responses {
			fmt.Println(results.Responses[i].Result)
			buffer.WriteString(results.Responses[i].Result)
		}

		_, err = historyBuffer.Write(buffer.Bytes())
		if err != nil {
			log.Fatal(err)
		}

		// Ask if user wants to continue
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Do you want to continue troubleshooting? (y/n): ")
		response, _ := reader.ReadString('\n')
		response = strings.ToLower(strings.TrimSpace(response))

		if response != "y" {
			break
		}
	}
}

func clearScreen() {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "cls")
	} else {
		cmd = exec.Command("clear")
	}
	cmd.Stdout = os.Stdout
	cmd.Run()
}
