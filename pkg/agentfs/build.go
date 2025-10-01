// Copyright 2025 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agentfs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progressui"
	"golang.org/x/sync/errgroup"
)

func (c *Client) Build(ctx context.Context, id string) error {
	params := url.Values{}
	params.Add("agent_id", id)
	fullUrl := fmt.Sprintf("%s/build?%s", c.agentsURL, params.Encode())
	req, err := c.newRequest("POST", fullUrl, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to build agent: %s", resp.Status)
	}

	ch := make(chan *bkclient.SolveStatus)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		display, err := progressui.NewDisplay(os.Stderr, "auto")
		if err != nil {
			return err
		}
		_, err = display.UpdateFrom(context.Background(), ch)
		return err
	})

	eg.Go(func() error {
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "BUILD ERROR:") {
				return errors.New(strings.TrimPrefix(line, "BUILD ERROR: "))
			}

			var status bkclient.SolveStatus
			if err := json.Unmarshal(scanner.Bytes(), &status); err != nil {
				return fmt.Errorf("decode error: %w", err)
			}
			select {
			case ch <- &status:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return scanner.Err()
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	return nil
}
