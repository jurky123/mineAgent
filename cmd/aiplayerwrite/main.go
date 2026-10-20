package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"mineagent/aiplayer"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	base := flag.String("base-url", "", "operator-pinned AI portal origin")
	token := flag.String("token-file", "", "independent owner-only writing token file")
	request := flag.String("request", "", "saved JSON request file, including stable idempotency key")
	flag.Parse()
	writer, err := aiplayer.NewWriter(*base, *token)
	if err != nil {
		return err
	}
	f, err := os.Open(*request)
	if err != nil {
		return errors.New("request file unavailable")
	}
	defer f.Close()
	var in aiplayer.WritingRequest
	dec := json.NewDecoder(io.LimitReader(f, 128<<10))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&in); err != nil {
		return errors.New("invalid writing request JSON")
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	result, err := writer.Do(ctx, in)
	if err != nil {
		return err
	}
	fmt.Println(string(result))
	return nil
}
