package investigation

import "context"

func (e *Engine) CheckIndexes(ctx context.Context) error {
	if err := e.verifyIndex(ctx); err != nil {
		return err
	}
	_, err := e.HistoryIndex(ctx, e.Config.HistoryAlias)
	return err
}
