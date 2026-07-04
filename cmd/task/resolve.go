package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	taskpb "todoapp/gen/task"
	"todoapp/gen/task/taskconnect"
)

// resolveTask turns a full id or unique id prefix into the task it names.
// Exact ids hit GetTask; anything else pages the whole task list (both
// completed states, no filter) collecting ids with that prefix. IDs are
// ULIDs, i.e. uppercase, so a lowercased prefix is matched uppercased too.
func resolveTask(ctx context.Context, tc taskconnect.TaskServiceClient, ref string) (*taskpb.Task, error) {
	res, err := tc.GetTask(ctx, connect.NewRequest(&taskpb.GetTaskRequest{Id: ref}))
	if err == nil {
		return res.Msg.GetTask(), nil
	}
	if c := connect.CodeOf(err); c != connect.CodeNotFound && c != connect.CodeInvalidArgument {
		return nil, err
	}

	upper := strings.ToUpper(ref)
	var matches []*taskpb.Task
	token := ""
	for {
		res, err := tc.ListTasks(ctx, connect.NewRequest(&taskpb.ListTasksRequest{
			PageSize:  1000,
			PageToken: token,
		}))
		if err != nil {
			return nil, err
		}
		for _, t := range res.Msg.GetTasks() {
			if strings.HasPrefix(t.GetId(), ref) || strings.HasPrefix(t.GetId(), upper) {
				matches = append(matches, t)
			}
		}
		token = res.Msg.GetNextPageToken()
		if token == "" {
			break
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("no task with id prefix %s", ref)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "id prefix %s matches %d tasks:", ref, len(matches))
	for _, t := range matches {
		fmt.Fprintf(&b, "\n  %s  %s", shortID(t.GetId()), t.GetTitle())
	}
	return nil, errors.New(b.String())
}
