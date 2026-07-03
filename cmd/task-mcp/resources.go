package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

const viewURIPrefix = "taskcore://view/"

// viewItemCap bounds how many items a view resource returns, matching the
// taskclient page size — an agent reading a view wants a survey, not a dump.
const viewItemCap = 200

// registerResources exposes read-only introspection surfaces: the view list,
// the kind/facet schema, and per-view item listings. Static resources are
// discoverable directly; the view/{name} template resolves any saved view,
// including the well-known inbox/today/completed.
func (b *bridge) registerResources(s *mcp.Server) {
	s.AddResource(&mcp.Resource{
		Name:        "views",
		URI:         "taskcore://views",
		MIMEType:    "application/json",
		Description: "The saved views (name, filter, description, order_by) as JSON. Read taskcore://view/{name} to get a view's items.",
	}, b.readViews)
	s.AddResource(&mcp.Resource{
		Name:        "schema",
		URI:         "taskcore://schema",
		MIMEType:    "application/json",
		Description: "The kind/facet registry (ListKinds) as JSON: every queryable kind and its facet bindings, so you can learn the shape items can take.",
	}, b.readSchema)
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "view",
		URITemplate: viewURIPrefix + "{name}",
		MIMEType:    "text/plain",
		Description: "Resolve a named view and return its items as protojson (one per line, capped at 200). Well-known names: inbox, today, completed.",
	}, b.readView)
}

func (b *bridge) readViews(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	resp, err := b.cl.Views().ListViews(ctx, &taskcorev1.ListViewsRequest{})
	if err != nil {
		return nil, rpcErr(err)
	}
	out, err := protojson.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return jsonResource(req.Params.URI, "application/json", out), nil
}

func (b *bridge) readSchema(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	resp, err := b.cl.Schema().ListKinds(ctx, &taskcorev1.ListKindsRequest{})
	if err != nil {
		return nil, rpcErr(err)
	}
	out, err := protojson.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return jsonResource(req.Params.URI, "application/json", out), nil
}

func (b *bridge) readView(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	name := strings.TrimPrefix(uri, viewURIPrefix)
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	if name == "" {
		return nil, fmt.Errorf("view name is required in %q", uri)
	}
	resp, err := b.cl.Views().GetView(ctx, &taskcorev1.GetViewRequest{Name: name})
	if err != nil {
		return nil, rpcErr(err)
	}
	view := resp.GetView()
	items, err := b.queryAllCapped(ctx, view.GetFilter(), view.GetOrderBy())
	if err != nil {
		return nil, err
	}
	txt, err := jsonLines(items)
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, "text/plain", []byte(txt)), nil
}

// queryAllCapped pages a view's filter through QueryAll, stopping at
// viewItemCap items to bound the response.
func (b *bridge) queryAllCapped(ctx context.Context, filter, orderBy string) ([]*taskcorev1.Item, error) {
	var items []*taskcorev1.Item
	errStop := errors.New("cap reached")
	_, err := b.cl.QueryAll(ctx, filter, orderBy, func(it *taskcorev1.Item) error {
		items = append(items, it)
		if len(items) >= viewItemCap {
			return errStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, rpcErr(err)
	}
	return items, nil
}

func jsonResource(uri, mime string, body []byte) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      uri,
		MIMEType: mime,
		Text:     string(body),
	}}}
}
