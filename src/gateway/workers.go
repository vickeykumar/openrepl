package gateway

import "tunnel"

// BindTunnel fills in the callbacks of a tunnel server's config so that
// workers become backends of the router as they register, stop being
// backends when they go offline, and the keys they announce are routed to
// them.
func (rt *Router) BindTunnel(cfg *tunnel.ServerConfig) {
	cfg.OnOnline = func(w *tunnel.Worker) {
		rt.AddBackend(NewRemoteBackend(RemoteConfig{
			ID:          w.ID(),
			Dial:        w.Dial,
			StripPrefix: rt.prefix,
			Weight:      w.Info().Weight,
			Languages:   w.Info().Languages,
			Capacity: func() (int64, int64) {
				return w.Used(), w.Info().Capacity
			},
			State: func() State {
				switch {
				case !w.Online():
					return Offline
				case w.Syncing():
					return Syncing
				case w.Draining():
					return Draining
				}
				return Online
			},
		}))
	}
	cfg.OnOffline = func(w *tunnel.Worker) {
		b, ok := rt.Backend(w.ID())
		if !ok {
			return
		}
		// Leave a backend that belongs to a newer connection of the same
		// worker alone.
		if rb, isRemote := b.(*RemoteBackend); isRemote && rb.State() == Offline {
			rt.RemoveBackend(b)
			rb.Close()
		}
	}
	cfg.OnRoute = func(w *tunnel.Worker, ev tunnel.RouteEvent, open bool) {
		if open {
			rt.routes.Set(ev.Kind, ev.Key, w.ID())
		} else {
			rt.routes.Delete(ev.Kind, ev.Key, w.ID())
		}
	}
}
