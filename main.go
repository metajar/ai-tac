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

type payload struct {
	Question string `json:"question"`
	Metadata string `json:"metadata"`
	Previous string `json:"previous_content"`
}

func main() {
	question := flag.String("question", "", "question")
	flag.Parse()

	for {
		p := payload{
			Question: *question,
			Metadata: testMetaData,
		}

		_, err := os.Stat("output.txt")
		if err == nil {
			bs, err := os.ReadFile("output.txt")
			if err != nil {
				log.Fatal(err)
			}
			p.Previous = string(bs)
		}

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
					"such as ping etc that will cause harm or cause the system to hang. Only give commands that are one command per line. If" +
					"you know what the issue is don't go any further and you need to say the stop phrase VIVACISCO and then give a detail" +
					"explanation of the problem and how one would resolve it. The output should be in markdown."),
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
		buffer.WriteString(chatCompletion.Choices[0].Message.Content)
		commandsToRun := strings.Split(chatCompletion.Choices[0].Message.Content, "\n")

		// Scrapli Setup
		pe, err := platform.NewPlatform(
			"cisco_iosxr",
			"172.20.20.3",
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
		// write the buffer to file
		file, err := os.OpenFile("output.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		_, err = file.Write(buffer.Bytes())
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
