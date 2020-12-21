package upnext

// Payload info https://github.com/im85288/service.upnext/wiki/Integration
type Payload struct {
	CurrentEpisode Episode `json:"current_episode"`
	NextEpisode    Episode `json:"next_episode"`
	PlayURL        string  `json:"play_url"`
}

// Episode info https://github.com/im85288/service.upnext/wiki/Integration
type Episode struct {
	EpisodeID  string `json:"episodeid"`
	TVShowID   string `json:"tvshowid"`
	Title      string `json:"title"`
	Art        Art    `json:"art"`
	Season     string `json:"season"`
	Episode    string `json:"episode"`
	ShowTitle  string `json:"showtitle"`
	Plot       string `json:"plot"`
	Playcount  int    `json:"playcount"`
	Rating     int    `json:"rating"`
	FirstAired string `json:"firstaired"`
	Runtime    int    `json:"runtime"`
}

// Art info https://github.com/im85288/service.upnext/wiki/Integration
type Art struct {
	Thumb           string `json:"thumb"`
	TVShowClearArt  string `json:"tvshow.clearart"`
	TVShowClearLogo string `json:"tvshow.clearlogo"`
	TVShowFanart    string `json:"tvshow.fanart"`
	TVShowLandscape string `json:"tvshow.landscape"`
	TVShowPoster    string `json:"tvshow.poster"`
}
