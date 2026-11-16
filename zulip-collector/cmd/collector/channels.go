package main

import (
	"context"
	"flag"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"zulip-collector/internal/source"
)

var cohortInChannelName = regexp.MustCompile(`(?i)(^|[^\pL\pN])БИВ[ -]?(22|23|24)\d*`)

func listChannels(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("collector channels", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("expected no positional arguments")
	}
	api, err := zulipFromEnv()
	if err != nil {
		return err
	}
	channels, err := api.PublicChannels(ctx)
	if err != nil {
		return err
	}
	slices.SortFunc(channels, func(a, b source.Channel) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, ch := range channels {
		if m := cohortInChannelName.FindStringSubmatch(ch.Name); m != nil {
			fmt.Printf("БИВ%s\t%d\t%s\n", m[2], ch.ID, ch.Name)
		}
	}
	return nil
}
