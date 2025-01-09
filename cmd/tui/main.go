package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/openai/openai-go"
	"github.com/scrapli/scrapligo/driver/options"
	"github.com/scrapli/scrapligo/platform"
)

// UI States
const (
	stateConfig       = iota // Initial configuration state
	stateTroubleshoot        // Troubleshooting state
)

type payload struct {
	Question string `json:"question"`
	Metadata string `json:"metadata"`
	Previous string `json:"previous_data"`
}

type connectionConfig struct {
	Hostname string
	Username string
	Password string
}

var (
	titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF75B7")).
		MarginLeft(2)

	statusMessageStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#04B575"))

	errorMessageStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF0000"))

	inputStyle = lipgloss.NewStyle().
		PaddingLeft(2).
		PaddingRight(2)

	focusedStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("205"))

	cursorStyle = focusedStyle.Copy()
)

type model struct {
	state         int
	configInputs  []textinput.Model
	question      textinput.Model
	viewport      viewport.Model
	spinner       spinner.Model
	err           error
	output        string
	ready         bool
	quitting      bool
	loading       bool
	metadata      string
	previousData  string
	firstQuestion bool
	explanation   string
	lastQuestion  string
	history       string
	config        connectionConfig
	currentInput  int
}

func initialModel() model {
	// Initialize configuration inputs
	configInputs := make([]textinput.Model, 3)
	for i := range configInputs {
		t := textinput.New()
		t.CharLimit = 64

		switch i {
		case 0:
			t.Placeholder = "Enter hostname (e.g., 172.20.20.3)"
			t.SetValue(getEnvWithDefault("NETWORK_HOST", "172.20.20.3"))
		case 1:
			t.Placeholder = "Enter username (e.g., clab)"
			t.SetValue(getEnvWithDefault("NETWORK_USER", "clab"))
		case 2:
			t.Placeholder = "Enter password"
			t.SetValue(getEnvWithDefault("NETWORK_PASS", "clab@123"))
			t.EchoMode = textinput.EchoPassword
			t.EchoCharacter = '•'
		}

		configInputs[i] = t
	}
	configInputs[0].Focus()

	// Initialize question input
	ti := textinput.New()
	ti.Placeholder = "Enter your network troubleshooting question..."
	ti.Width = 50
	ti.CharLimit = 156

	// Initialize viewport
	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		PaddingRight(2)

	// Initialize spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return model{
		state:         stateConfig,
		configInputs:  configInputs,
		question:      ti,
		spinner:       s,
		viewport:      vp,
		metadata:      `{"router_type": "Cisco XRv 9000", "Virtual": true}`,
		loading:       false,
		firstQuestion: true,
		currentInput:  0,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

type outputMsg string

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case stateConfig:
			switch msg.String() {
			case "ctrl+c", "q":
				m.quitting = true
				return m, tea.Quit
			case "tab", "shift+tab", "enter", "up", "down":
				// Handle input navigation
				s := msg.String()

				if s == "enter" && m.currentInput == len(m.configInputs)-1 {
					// Save configuration and switch to troubleshooting state
					m.config = connectionConfig{
						Hostname: m.configInputs[0].Value(),
						Username: m.configInputs[1].Value(),
						Password: m.configInputs[2].Value(),
					}
					m.state = stateTroubleshoot
					m.question.Focus()
					return m, nil
				}

				// Cycle between inputs
				if s == "up" || s == "shift+tab" {
					m.currentInput--
				} else {
					m.currentInput++
				}

				if m.currentInput >= len(m.configInputs) {
					m.currentInput = 0
				}
				if m.currentInput < 0 {
					m.currentInput = len(m.configInputs) - 1
				}

				for i := 0; i < len(m.configInputs); i++ {
					if i == m.currentInput {
						m.configInputs[i].Focus()
					} else {
						m.configInputs[i].Blur()
					}
				}

				return m, nil
			}

		case stateTroubleshoot:
			switch msg.String() {
			case "ctrl+c", "q":
				m.quitting = true
				return m, tea.Quit
			case "enter":
				if m.question.Value() != "" || !m.firstQuestion {
					m.loading = true
					if m.question.Value() == "" {
						m.question.SetValue(m.lastQuestion)
					} else {
						m.lastQuestion = m.question.Value()
					}
					return m, m.processQuestion
				}
			}
		}

	case tea.WindowSizeMsg:
		if !m.ready {
			m.viewport = viewport.New(msg.Width-4, msg.Height-7)
			m.viewport.Style = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("62")).
				PaddingRight(2)
			m.ready = true
		}

	case error:
		m.err = msg
		m.loading = false
		return m, nil

	case outputMsg:
		if m.explanation != "" {
			m.output = string(msg)
		} else {
			m.output += string(msg)
		}
		m.viewport.SetContent(m.output)
		m.loading = false
		m.question.Reset()
		if m.firstQuestion {
			m.firstQuestion = false
			m.question.Placeholder = "Press Enter to continue troubleshooting..."
		}
		return m, nil
	}

	if m.loading {
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update appropriate inputs based on state
	if m.state == stateConfig {
		for i := range m.configInputs {
			m.configInputs[i], cmd = m.configInputs[i].Update(msg)
			cmds = append(cmds, cmd)
		}
	} else {
		m.question, cmd = m.question.Update(msg)
		cmds = append(cmds, cmd)
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	if !m.ready {
		return "\n  Initializing..."
	}

	switch m.state {
	case stateConfig:
		var b strings.Builder

		b.WriteString(titleStyle.Render("Network Device Configuration"))
		b.WriteString("\n\n")

		for i := range m.configInputs {
			b.WriteString(m.configInputs[i].View())
			b.WriteString("\n")
		}

		b.WriteString("\n")
		b.WriteString(statusMessageStyle.Render("Press Tab to cycle through inputs • Enter to confirm"))

		return b.String()

	case stateTroubleshoot:
		var status string
		if m.err != nil {
			status = errorMessageStyle.Render(fmt.Sprintf("Error: %v", m.err))
		} else if m.loading {
			status = m.spinner.View() + " Processing..."
		} else if m.explanation != "" {
			status = statusMessageStyle.Render("Issue found! Press 'q' to exit")
		} else if m.firstQuestion {
			status = statusMessageStyle.Render("Enter your troubleshooting question and press Enter")
		} else {
			status = statusMessageStyle.Render("Press Enter to continue troubleshooting")
		}

		return fmt.Sprintf(
			"\n%s\n\n%s\n\n%s\n%s",
			titleStyle.Render("Network Troubleshooting Assistant"),
			m.viewport.View(),
			m.question.View(),
			status,
		)
	}

	return ""
}

func (m *model) processQuestion() tea.Msg {
	p := payload{
		Question: m.question.Value(),
		Metadata: m.metadata,
		Previous: m.history,
	}

	payloadBytes, err := json.Marshal(p)
	if err != nil {
		return err
	}

	client := openai.NewClient()
	chatCompletion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a network engineer that troubleshoots networking issues. You only" +
				"ever will return commands with the problem that can be ran and you WILL NEVER, I REPEAT, NEVER return" +
				"any command that will alter the configuration, any debug commands, or any other command known to cause issues" +
				"such as ping etc that will cause harm or cause the system to hang. Only give commands that are one command per line. " +
				"Do not use any type of show log commands unless you use the | inc with the exact thing you are looking for. If" +
				"you know what the issue is don't go any further and you need to say the stop phrase VIVACISCO and then give a detail" +
				"explanation of the problem and how one would resolve it. The output should be in markdown. "),
			openai.UserMessage(string(payloadBytes)),
		}),
		Model: openai.F(openai.ChatModelGPT4o),
	})
	if err != nil {
		return err
	}

	if strings.Contains(chatCompletion.Choices[0].Message.Content, "VIVACISCO") {
		explanation := strings.Split(chatCompletion.Choices[0].Message.Content, "VIVACISCO")[1]
		m.explanation = explanation
		// Clear viewport but keep history
		m.output = ""
		out, err := glamour.Render("# AI TAC EXPLANATION\n"+explanation, "dark")
		if err != nil {
			return err
		}
		return outputMsg(out + "\n\nPress 'q' to exit")
	}

	var buffer strings.Builder
	buffer.WriteString("\n# Commands to execute:\n")
	buffer.WriteString(chatCompletion.Choices[0].Message.Content)
	buffer.WriteString("\n\n# Results:\n")

	commandsToRun := strings.Split(chatCompletion.Choices[0].Message.Content, "\n")

	pe, err := platform.NewPlatform(
		"cisco_iosxr",
		m.config.Hostname,
		options.WithAuthNoStrictKey(),
		options.WithAuthUsername(m.config.Username),
		options.WithAuthPassword(m.config.Password),
	)
	if err != nil {
		return fmt.Errorf("failed to create platform: %v", err)
	}

	d, err := pe.GetNetworkDriver()
	if err != nil {
		return fmt.Errorf("failed to fetch network driver: %v", err)
	}

	err = d.Open()
	if err != nil {
		return fmt.Errorf("failed to open driver: %v", err)
	}
	defer d.Close()

	results, err := d.SendCommands(commandsToRun)
	if err != nil {
		return fmt.Errorf("failed to send commands: %v", err)
	}
	for i := range results.Responses {
		buffer.WriteString(results.Responses[i].Result)
	}
	m.history += buffer.String()
	renderedOutput, err := glamour.Render(buffer.String(), "dark")
	if err != nil {
		return err
	}

	return outputMsg(renderedOutput)
}

func getEnvWithDefault(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func main() {
	m := initialModel()
	p := tea.NewProgram(&m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v", err)
		os.Exit(1)
	}
}
