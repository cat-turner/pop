package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/myelnet/go-hop-exchange/node"
	"github.com/peterbourgon/ff/v2/ffcli"
)

var getArgs struct {
	selector string
	output   string
	timeout  int
	verbose  bool
}

var getCmd = &ffcli.Command{
	Name:       "get",
	ShortUsage: "get <cid>",
	ShortHelp:  "Retrieve content from the network",
	LongHelp: strings.TrimSpace(`

The 'hop get' command retrieves blocks with a given root cid and an optional selector
(defaults retrieves all the linked blocks). Passing an output flag with a path will write the
data to disk.

`),
	Exec: runGet,
	FlagSet: (func() *flag.FlagSet {
		fs := flag.NewFlagSet("get", flag.ExitOnError)
		fs.StringVar(&getArgs.selector, "selector", "all", "select blocks to retrieve for a root cid")
		fs.StringVar(&getArgs.output, "output", "", "write the file to the path")
		fs.IntVar(&getArgs.timeout, "timeout", 5, "timeout before the request should be cancelled by the node (in minutes)")
		fs.BoolVar(&getArgs.verbose, "verbose", false, "print the state transitions")
		return fs
	})(),
}

func runGet(ctx context.Context, args []string) error {
	c, cc, ctx, cancel := connect(ctx)
	defer cancel()

	grc := make(chan *node.GetResult)
	cc.SetNotifyCallback(func(n node.Notify) {
		if gr := n.GetResult; gr != nil {
			grc <- gr
		}
	})
	go receive(ctx, cc, c)

	cc.Get(&node.GetArgs{
		Cid:     args[0],
		Timeout: getArgs.timeout,
		Sel:     getArgs.selector,
		Out:     getArgs.output,
		Verbose: getArgs.verbose,
	})
	at := time.Now()
	for {
		select {
		case gr := <-grc:
			if gr.Err != "" {
				return errors.New(gr.Err)
			}
			if gr.DealID != "" {
				fmt.Printf("started retrieval deal %s\n", gr.DealID)
				continue
			}
			now := time.Now()
			delay := now.Sub(at)
			// TODO: print latency and other metadata
			fmt.Printf("get operation completed in %v\n", delay.Seconds())
			return nil
		case <-ctx.Done():
			return fmt.Errorf("Get operation timed out")
		}
	}
}
