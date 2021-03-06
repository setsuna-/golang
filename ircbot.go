package main

import (
  "bufio"
  "encoding/json"
  "fmt"
  "io/ioutil"
  "math/rand"
  "net"
  "net/http"
  "os"
  "path/filepath"
  "regexp"
  "strconv"
  "strings"
  "time"
)

const version = "Golang IRC Bot v0.10.0"

// Config struct
type Config struct {
  Bot struct {
    Nickname string
    Ident    string
    Realname string
  }
  Server struct {
    Host    string
    Port    string
    Channel string
  }
  LogFile    string
  PidFile    string
  Owner      string
  Debug      bool
  DistrosURL string
  DetailsURL string
}

// Message struct
type Message struct {
  From   string
  Action string
  To     string
  Text   string
}

// Log struct
type Log struct {
  fp *os.File
}

var currentNickname string

func main() {

  binaryPath, _ := os.Executable()
  binaryDir := filepath.Dir(binaryPath)

  config := readConfig(binaryDir + "/ircbot-config.json")
  writePid(binaryDir + "/" + config.PidFile)

  log := openLog(binaryDir + "/" + config.LogFile)
  log.Put(fmt.Sprintf("Connecting to %v", config.Server.Host+":"+config.Server.Port))

  conn, err := net.Dial("tcp", config.Server.Host+":"+config.Server.Port)
  if err != nil {
    log.Put(err.Error())
    panic(err)
  }

  register(conn, log, config)
  for {
    message, err := bufio.NewReader(conn).ReadString('\n')
    if err != nil {
      log.Put(err.Error())
      panic(err)
    }
    message = strings.TrimSpace(message)
    handleRawMessage(conn, log, config, message)
  }

}

// Put method on Log
func (log Log) Put(text string) {
  prefix := time.Now().Format("2006-01-02 15:04:05")
  fmt.Printf("[%s] %s\n", prefix, text)
  _, err := fmt.Fprintf(log.fp, "[%s] %s\n", prefix, text)
  if err != nil {
    panic(err)
  }
}

func readConfig(filename string) Config {
  file, err := os.Open(filename)
  if err != nil {
    panic(err)
  }
  defer file.Close()
  decoder := json.NewDecoder(file)
  Config := Config{}
  err = decoder.Decode(&Config)
  if err != nil {
    panic(err)
  }
  return Config
}

func register(conn net.Conn, log Log, config Config) {
  currentNickname = config.Bot.Nickname
  send(conn, log, fmt.Sprintf("USER %s 0 * :%s", config.Bot.Ident, config.Bot.Realname))
  send(conn, log, fmt.Sprintf("NICK %s", currentNickname))
  send(conn, log, fmt.Sprintf("JOIN %s", config.Server.Channel))
}

func handleRawMessage(conn net.Conn, log Log, config Config, rawMessage string) {
  if strings.HasPrefix(rawMessage, "PING :") { // PING-PONG is useless in logs
    fmt.Fprintf(conn, "%s\n", strings.Replace(rawMessage, "PING :", "PONG :", -1))
    return
  }
  log.Put(rawMessage)
  if strings.HasPrefix(rawMessage, ":") {
    message := parseRawMessage(rawMessage)
    if config.Debug {
      debug(message)
    }
    if message.Action == "001" { // Welcome message
      send(conn, log, fmt.Sprintf("JOIN %s", config.Server.Channel))
      return
    }
    if message.Action == "433" { // Nick in use
      currentNickname = regenerateNickname(config.Bot.Nickname)
      send(conn, log, fmt.Sprintf("NICK %s", currentNickname))
      return
    }
    if message.Action == "INVITE" {
      send(conn, log, "JOIN "+message.Text)
      return
    }
    if message.Action == "JOIN" {
      if strings.HasPrefix(message.From, currentNickname) {
        send(conn, log, "PRIVMSG "+message.To+" :Bonjour tlm !")
      } else {
        nickname := strings.Split(message.From, "!")[0]
        send(conn, log, "PRIVMSG "+message.To+" :Bonjour "+nickname+" !")
      }
      return
    }
    if message.Action == "KICK" && strings.HasPrefix(message.Text, currentNickname) {
      send(conn, log, "JOIN "+message.To)
      return
    }
    if message.Action == "PRIVMSG" && message.To == currentNickname && strings.Contains(message.Text, "VERSION") {
      nickname := strings.Split(message.From, "!")[0]
      send(conn, log, "NOTICE "+nickname+" :"+version)
      return
    }
    if strings.HasPrefix(message.From, config.Owner) &&
      message.To == currentNickname &&
      strings.HasPrefix(message.Text, "raw") {
      command := strings.Join(strings.Split(message.Text, " ")[1:], " ")
      send(conn, log, command)
      return
    }
    if strings.Contains(message.Text, "http") {
      urls := treatUrls(message.Text)
      for url, title := range urls {
        send(conn, log, "PRIVMSG "+message.To+" :["+url+"] "+title)
      }
      return
    }
  }
}

func send(conn net.Conn, log Log, rawMessage string) {
  log.Put(rawMessage)
  fmt.Fprintf(conn, "%s\n", rawMessage)
}

func parseRawMessage(message string) Message {
  parts := strings.Split(message, " ")
  from := strings.TrimPrefix(parts[0], ":")
  action := parts[1]
  to := strings.TrimSpace(strings.TrimPrefix(parts[2], ":"))
  text := strings.TrimSpace(strings.TrimPrefix(strings.Join(parts[3:], " "), ":"))
  return Message{from, action, to, text}
}

func openLog(file string) Log {
  f, err := os.OpenFile(file,
    os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
  if err != nil {
    panic(err)
  }
  return Log{f}
}

func regenerateNickname(originalNickname string) string {
  rand.Seed(time.Now().UnixNano())
  suffix := strconv.FormatInt(rand.Int63n(10000), 10)
  return originalNickname + suffix
}

func debug(message Message) {
  fmt.Println("--- DEBUG ---")
  fmt.Println("  FROM: -" + message.From + "-")
  fmt.Println("  ACTION: -" + message.Action + "-")
  fmt.Println("  TO: -" + message.To + "-")
  fmt.Println("  TEXT: -" + message.Text + "-")
  fmt.Println("--- END DEBUG ---")
}

func treatUrls(text string) map[string]string {
  urls := make(map[string]string)
  re := regexp.MustCompile("http[s]?://[^\\s]+")
  matches := re.FindAllString(text, -1)
  if len(matches) == 0 {
    return urls
  }
  for _, url := range matches {
    body := safeDownload(url)
    title := extractTitle(body)
    if len(title) > 0 {
      urls[url] = title
    }
  }
  return urls
}

func safeDownload(url string) string {
  req, _ := http.NewRequest("GET", url, nil)
  req.Header.Add("Range", "bytes=0-2048")
  var client http.Client
  resp, err := client.Do(req)
  if err != nil {
    return ""
  }
  if !strings.Contains(resp.Header.Get("Content-Type"), "html") {
    return ""
  }
  defer resp.Body.Close()
  body, err := ioutil.ReadAll(resp.Body)
  if err != nil {
    return ""
  }
  return string(body)
}

func extractTitle(body string) string {
  re := regexp.MustCompile(`(?i)(?:<title>)(.+)(?:</title>)`)
  matches := re.FindStringSubmatch(body)
  if matches == nil {
    return ""
  }
  return matches[1]
}

func getMapValues(strMap map[string]string) []string {
  var values []string
  for _, value := range strMap {
    values = append(values, value)
  }
  return values
}

func getMapKey(strMap map[string]string, search string) string {
  for key, value := range strMap {
    if strings.ToLower(value) == strings.ToLower(search) {
      return key
    }
  }
  return ""
}

func writePid(pidFile string) {
  pid := []byte(strconv.Itoa(os.Getpid()))
  err := ioutil.WriteFile(pidFile, pid, 0644)
  if err != nil {
    panic(err.Error)
  }
}
