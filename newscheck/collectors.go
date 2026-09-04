package newscheck

import (
	"context"
	"fmt"
	"time"

	"urban-radar/tgl"
	"urban-radar/zakupki"
)

type TGLClient interface {
	ListNewsWindow(context.Context, time.Time, time.Time, int) (tgl.WindowResult, error)
	GetNews(context.Context, string) (tgl.Article, error)
}

type TGLCollector struct{ Client TGLClient }

func (TGLCollector) Name() string { return "tgl" }

func (c TGLCollector) Collect(ctx context.Context, window Window) (Collection, error) {
	result, err := c.Client.ListNewsWindow(ctx, window.From, window.To, window.MaxPages)
	collection := Collection{PagesRead: result.PagesRead, Complete: result.Complete}
	if err != nil {
		return collection, err
	}
	for _, item := range result.News {
		article, articleErr := c.Client.GetNews(ctx, item.URL)
		if articleErr != nil {
			return collection, fmt.Errorf("TGL article %s: %w", item.URL, articleErr)
		}
		sourceItem := item.SourceItem(window.To)
		sourceItem.Text = article.Text
		collection.Items = append(collection.Items, sourceItem)
	}
	return collection, nil
}

type ZakupkiSearch interface {
	FetchWindow(context.Context, zakupki.SearchOptions) (zakupki.SearchFetchResult, error)
}

type ZakupkiCollector struct {
	Search      ZakupkiSearch
	CardFetcher zakupki.ProcurementCardFetcher
}

func (ZakupkiCollector) Name() string { return zakupki.SourceName }

func (c ZakupkiCollector) Collect(ctx context.Context, window Window) (Collection, error) {
	result, err := c.Search.FetchWindow(ctx, zakupki.SearchOptions{PublishDateFrom: window.From, PublishDateTo: window.To, MaxPages: window.MaxPages})
	collection := Collection{PagesRead: result.PagesRead, Complete: result.Complete}
	if err != nil {
		return collection, err
	}
	for index, raw := range result.Raw {
		procurement, parseErr := zakupki.ParseRaw(raw)
		if parseErr != nil {
			return collection, fmt.Errorf("Zakupki item %d: %w", index+1, parseErr)
		}
		card, cardErr := c.CardFetcher.FetchCard(ctx, procurement.URL)
		if cardErr != nil {
			return collection, fmt.Errorf("Zakupki card %s: %w", procurement.ID, cardErr)
		}
		procurement, cardErr = zakupki.ParseProcurementCard(card, procurement)
		if cardErr != nil {
			return collection, fmt.Errorf("Zakupki card %s: %w", procurement.ID, cardErr)
		}
		collection.Items = append(collection.Items, procurement.SourceItem(window.To))
	}
	return collection, nil
}
