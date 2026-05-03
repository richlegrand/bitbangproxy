// BitBangProxy - WebRTC proxy for local web servers.
//
// Connects to the BitBang signaling server and proxies browser requests
// to a local web server via WebRTC data channels.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"sync"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/richlegrand/bitbang/bitbangproxy/internal/auth"
	"github.com/richlegrand/bitbang/bitbangproxy/internal/identity"
	"github.com/richlegrand/bitbang/bitbangproxy/internal/peer"
	"github.com/richlegrand/bitbang/bitbangproxy/internal/proxy"
	"github.com/richlegrand/bitbang/bitbangproxy/internal/signaling"
)

const version = "0.1.2"

const banner = `   ___  _ __  ___                 ___
  / _ )(_) /_/ _ )___ ____  ___ _/ _ \_______ __ ____ __
 / _  / / __/ _  / _ ` + "`" + `/ _ \/ _ ` + "`" + `/ ___/ __/ _ \\ \ / // /
/____/_/\__/____/\_,_/_//_/\_, /_/  /_/  \___/_\_\\_, /
                          /___/                  /___/ `

func main() {
	server := flag.String("server", "bitba.ng", "Signaling server hostname")
	target := flag.String("target", "", "Local server to proxy (e.g. localhost:8080). If not set, target is extracted from the URL.")
	pin := flag.String("pin", "", "PIN to protect proxy access")
	ephemeral := flag.Bool("ephemeral", false, "Use a temporary identity (not saved to disk)")
	verbose := flag.Bool("v", false, "Verbose logging")
	flag.Parse()

	pinAuth := auth.New(*pin)

	// Load or create identity
	id, err := identity.Load("bitbangproxy", *ephemeral)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Identity error: %v\n", err)
		os.Exit(1)
	}

	url := fmt.Sprintf("https://%s/%s", *server, id.UID)
	if *verbose {
		url += "?debug"
	}

	fmt.Println(banner)
	fmt.Printf("v%s\n", version)
	if *verbose {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, dep := range info.Deps {
				fmt.Printf("  %s %s\n", dep.Path, dep.Version)
			}
		}
		fmt.Printf("  %s\n", runtime.Version())
	}
	fmt.Println()

	// Print QR code
	qr, err := qrcode.New(url, qrcode.Medium)
	if err == nil {
		fmt.Println(qr.ToSmallString(false))
	}

	fmt.Printf("URL: %s\n", url)
	if *target != "" {
		fmt.Printf("Proxying: %s\n", *target)
	} else {
		fmt.Printf("Proxying: dynamic (target from URL)\n")
	}
	if pinAuth.Required() {
		fmt.Printf("PIN protection enabled\n")
	}
	fmt.Println()

	// Track active peer connections by client_id
	var mu sync.Mutex
	connections := make(map[string]*peer.Connection)

	// Connect to signaling server
	client := signaling.NewClient(*server, id)
	client.Verbose = *verbose

	client.Connect(func(msg signaling.Message) {
		msgType, _ := msg["type"].(string)

		switch msgType {
		case "request":
			clientID, _ := msg["client_id"].(string)
			log.Printf("Connection request from %s", clientID)

			// Create a proxy handler that will be wired to the data channel
			var handler *proxy.Handler

			conn, err := peer.HandleRequest(msg, client, func(data []byte) {
				if handler != nil {
					handler.HandleMessage(data)
				}
			}, *verbose)
			if err != nil {
				log.Printf("Failed to create peer connection: %v", err)
				return
			}

			// Wire the handler to the data channel
			handler = &proxy.Handler{
				Target:  *target,
				UID:     id.UID,
				Server:  *server,
				PIN:     pinAuth,
				Verbose: *verbose,
				DC:      conn.DC,
			}

			mu.Lock()
			connections[clientID] = conn
			mu.Unlock()

		case "answer":
			clientID, _ := msg["client_id"].(string)
			sdp, _ := msg["sdp"].(string)

			mu.Lock()
			conn := connections[clientID]
			mu.Unlock()

			if conn == nil {
				if *verbose {
					log.Printf("Answer for unknown client: %s", clientID)
				}
				return
			}

			if err := conn.HandleAnswer(sdp); err != nil {
				log.Printf("Failed to handle answer for %s: %v", clientID, err)
			}

		case "candidate":
			clientID, _ := msg["client_id"].(string)
			candidateData, _ := msg["candidate"].(map[string]interface{})

			mu.Lock()
			conn := connections[clientID]
			mu.Unlock()

			if conn == nil {
				if *verbose {
					log.Printf("Candidate for unknown client: %s", clientID)
				}
				return
			}

			if err := conn.AddICECandidate(candidateData); err != nil {
				if *verbose {
					log.Printf("Failed to add candidate for %s: %v", clientID, err)
				}
			}

		case "error":
			log.Printf("Signaling error: %v", msg["message"])

		default:
			if *verbose {
				log.Printf("Unknown message type: %s", msgType)
			}
		}
	})
}
