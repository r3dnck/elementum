package library

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash"
	"github.com/karrick/godirwalk"

	"github.com/elgatito/elementum/cache"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/playcount"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/trakt"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

var (
	movieRegexp = regexp.MustCompile(`^plugin://plugin.video.elementum.*/movie/\w+/(\d+)`)
	showRegexp  = regexp.MustCompile(`^plugin://plugin.video.elementum.*/show/\w+/(\d+)/(\d+)/(\d+)`)
)

// RefreshOnScan is launched when scan is finished
func RefreshOnScan() error {
	l.Pending.IsOverall = true
	l.Running.IsKodi = false

	return nil
}

// RefreshKodi runs Kodi library refresh
func RefreshKodi() error {
	if l.Running.IsKodi {
		return nil
	}

	l.Running.IsKodi = true
	l.Pending.IsKodi = false
	xbmc.VideoLibraryScan()
	l.Running.IsKodi = false

	return nil
}

// Refresh is updating library from Kodi
func Refresh() error {
	if l.Running.IsTrakt || !config.Get().LibraryEnabled {
		return nil
	}

	l.Pending.IsOverall = false
	l.Running.IsOverall = true
	defer func() {
		l.Running.IsOverall = false
		util.FreeMemoryGC()
	}()

	now := time.Now()

	if err := RefreshMovies(); err != nil {
		log.Debugf("RefreshMovies got an error: %v", err)
	}
	if err := RefreshShows(); err != nil {
		log.Debugf("RefreshShows got an error: %v", err)
	}

	log.Debugf("Library refresh finished in %s", time.Since(now))
	return nil
}

// RefreshMovies updates movies in the library
func RefreshMovies() error {
	if l.Running.IsMovies || l.Running.IsKodi || !config.Get().LibraryEnabled || !config.Get().LibrarySyncEnabled || (!config.Get().LibrarySyncPlaybackEnabled && xbmc.PlayerIsPlaying()) {
		return nil
	}

	l.Pending.IsMovies = false
	l.Running.IsMovies = true

	defer func() {
		l.Running.IsMovies = false
		RefreshUIDs()
	}()

	started := time.Now()
	movies, err := xbmc.VideoLibraryGetMovies()
	if err != nil {
		return err
	} else if movies != nil && movies.Limits != nil && movies.Limits.Total == 0 {
		return nil
	} else if movies == nil || movies.Movies == nil {
		return errors.New("Could not fetch Movies from Kodi")
	}

	defer func() {
		log.Debugf("Fetched %d movies from Kodi Library in %s", len(movies.Movies), time.Since(started))
	}()

	l.mu.Movies.Lock()
	defer l.mu.Movies.Unlock()

	l.Movies = make([]*Movie, 0, len(movies.Movies))
	for _, m := range movies.Movies {
		m.UniqueIDs.Kodi = m.ID
		if m.UniqueIDs.IMDB == "" && m.IMDBNumber != "" && strings.HasPrefix(m.IMDBNumber, "tt") {
			m.UniqueIDs.IMDB = m.IMDBNumber
		}

		lm := &Movie{
			ID:        m.ID,
			Title:     m.Title,
			File:      m.File,
			Year:      m.Year,
			DateAdded: m.DateAdded.Time,
			Resume:    &Resume{},
			UIDs:      &UniqueIDs{Kodi: m.ID, Playcount: m.PlayCount},
			XbmcUIDs:  &m.UniqueIDs,
		}

		if m.Resume != nil {
			lm.Resume.Position = m.Resume.Position
			lm.Resume.Total = m.Resume.Total
		}

		l.Movies = append(l.Movies, lm)
	}

	for _, m := range l.Movies {
		parseUniqueID(MovieType, m.UIDs, m.XbmcUIDs, m.File, m.Year)
	}

	return nil
}

// RefreshShows updates shows in the library
func RefreshShows() error {
	if l.Running.IsShows || l.Running.IsKodi || !config.Get().LibraryEnabled || !config.Get().LibrarySyncEnabled || (!config.Get().LibrarySyncPlaybackEnabled && xbmc.PlayerIsPlaying()) {
		return nil
	}

	l.Pending.IsShows = false
	l.Running.IsShows = true

	defer func() {
		l.Running.IsShows = false
		RefreshUIDs()
	}()

	started := time.Now()
	shows, err := xbmc.VideoLibraryGetShows()
	if err != nil {
		return err
	} else if shows != nil && shows.Limits != nil && shows.Limits.Total == 0 {
		return nil
	} else if shows == nil || shows.Shows == nil {
		return errors.New("Could not fetch Shows from Kodi")
	}

	defer func() {
		log.Debugf("Fetched %d shows from Kodi Library in %s", len(shows.Shows), time.Since(started))
	}()

	l.mu.Shows.Lock()
	l.Shows = make([]*Show, 0, len(shows.Shows))
	for _, s := range shows.Shows {
		s.UniqueIDs.Kodi = s.ID
		if s.UniqueIDs.IMDB == "" && s.IMDBNumber != "" && strings.HasPrefix(s.IMDBNumber, "tt") {
			s.UniqueIDs.IMDB = s.IMDBNumber
		}

		l.Shows = append(l.Shows, &Show{
			ID:        s.ID,
			Title:     s.Title,
			Seasons:   []*Season{},
			Episodes:  []*Episode{},
			Year:      s.Year,
			DateAdded: s.DateAdded.Time,
			UIDs:      &UniqueIDs{Kodi: s.ID, Playcount: s.PlayCount},
			XbmcUIDs:  &s.UniqueIDs,
		})

		parseUniqueID(ShowType, l.Shows[len(l.Shows)-1].UIDs, l.Shows[len(l.Shows)-1].XbmcUIDs, "", l.Shows[len(l.Shows)-1].Year)
	}
	l.mu.Shows.Unlock()

	if err := RefreshSeasons(); err != nil {
		log.Debugf("RefreshSeasons got an error: %v", err)
	}
	if err := RefreshEpisodes(); err != nil {
		log.Debugf("RefreshEpisodes got an error: %v", err)
	}

	// TODO: This needs refactor to avoid setting global Lock on processing,
	// should use temporary container to process and then sync to Shows
	l.mu.Shows.Lock()
	for _, show := range l.Shows {
		// Step 1: try to get information from what we get from Kodi
		parseUniqueID(ShowType, show.UIDs, show.XbmcUIDs, "", show.Year)

		// Step 2: if TMDB not found - try to find it from episodes
		if show.UIDs.TMDB == 0 {
			for _, e := range show.Episodes {
				if !strings.HasSuffix(e.File, ".strm") {
					continue
				}

				u := &UniqueIDs{}
				parseUniqueID(EpisodeType, u, e.XbmcUIDs, e.File, 0)
				if u.TMDB != 0 {
					show.UIDs.TMDB = u.TMDB
					break
				}
			}
		}

		if show.UIDs.TMDB == 0 {
			continue
		}

		for _, s := range show.Seasons {
			s.UIDs.TMDB = show.UIDs.TMDB
			s.UIDs.TVDB = show.UIDs.TVDB
			s.UIDs.IMDB = show.UIDs.IMDB

			parseUniqueID(SeasonType, s.UIDs, s.XbmcUIDs, "", 0)
		}
		for _, e := range show.Episodes {
			e.UIDs.TMDB = show.UIDs.TMDB
			e.UIDs.TVDB = show.UIDs.TVDB
			e.UIDs.IMDB = show.UIDs.IMDB

			parseUniqueID(EpisodeType, e.UIDs, e.XbmcUIDs, "", 0)
		}
	}
	l.mu.Shows.Unlock()

	return nil
}

// RefreshSeasons updates seasons list for selected show in the library
func RefreshSeasons() error {
	started := time.Now()

	// Collect all shows IDs for possibly doing one-by-one calls to Kodi
	l.mu.Shows.Lock()
	shows := make([]int, 0, len(l.Shows))
	for _, s := range l.Shows {
		shows = append(shows, s.ID)
	}
	l.mu.Shows.Unlock()

	seasons, err := xbmc.VideoLibraryGetAllSeasons(shows)
	if err != nil {
		return err
	} else if seasons == nil || seasons.Seasons == nil {
		return errors.New("Could not fetch Seasons from Kodi")
	}

	defer func() {
		log.Debugf("Fetched %d seasons from Kodi Library in %s", len(seasons.Seasons), time.Since(started))
	}()

	l.mu.Shows.Lock()
	cleanupCheck := map[int]bool{}
	for _, s := range seasons.Seasons {
		c, err := findShowByKodi(s.TVShowID)
		if err != nil || c == nil || c.Seasons == nil {
			continue
		}

		if _, ok := cleanupCheck[s.TVShowID]; !ok {
			c.Seasons = []*Season{}
			cleanupCheck[s.TVShowID] = true
		}

		s.UniqueIDs.Kodi = s.ID

		c.Seasons = append(c.Seasons, &Season{
			ID:       s.ID,
			Title:    s.Title,
			Season:   s.Season,
			Episodes: s.Episodes,
			UIDs:     &UniqueIDs{Kodi: s.ID, Playcount: s.PlayCount},
			XbmcUIDs: &s.UniqueIDs,
		})
	}
	l.mu.Shows.Unlock()

	return nil
}

// RefreshEpisodes updates episodes list for selected show in the library
func RefreshEpisodes() error {
	if !l.Running.IsShows && len(pendingShows) == 0 {
		return nil
	}

	l.Pending.IsEpisodes = false
	l.Running.IsEpisodes = true

	defer func() {
		l.Running.IsEpisodes = false
	}()

	started := time.Now()

	var shows []int
	if len(pendingShows) != 0 {
		defer func() {
			RefreshUIDs()
		}()

		shows = make([]int, 0, len(pendingShows))
		for i := range pendingShows {
			shows = append(shows, i)
		}
		pendingShows = map[int]bool{}
	} else {
		l.mu.Shows.Lock()
		shows = make([]int, 0, len(l.Shows))
		for _, i := range l.Shows {
			shows = append(shows, i.XbmcUIDs.Kodi)
		}
		l.mu.Shows.Unlock()
	}

	episodes, err := xbmc.VideoLibraryGetAllEpisodes(shows)
	if err != nil {
		return err
	} else if episodes == nil || episodes.Episodes == nil {
		return errors.New("Could not fetch Episodes from Kodi")
	}

	defer func() {
		log.Debugf("Fetched %d episodes from Kodi Library in %s", len(episodes.Episodes), time.Since(started))
	}()

	l.mu.Shows.Lock()
	cleanupCheck := map[int]bool{}
	for _, e := range episodes.Episodes {
		c, err := findShowByKodi(e.TVShowID)
		if err != nil || c == nil || c.Episodes == nil {
			continue
		}

		if _, ok := cleanupCheck[e.TVShowID]; !ok {
			c.Episodes = []*Episode{}
			cleanupCheck[e.TVShowID] = true
		}

		e.UniqueIDs.Kodi = e.ID
		e.UniqueIDs.TMDB = ""
		e.UniqueIDs.TVDB = ""
		e.UniqueIDs.Trakt = ""
		e.UniqueIDs.Unknown = ""

		c.Episodes = append(c.Episodes, &Episode{
			ID:        e.ID,
			Title:     e.Title,
			Season:    e.Season,
			Episode:   e.Episode,
			File:      e.File,
			DateAdded: e.DateAdded.Time,
			Resume:    &Resume{},
			UIDs:      &UniqueIDs{Kodi: e.ID, Playcount: e.PlayCount},
			XbmcUIDs:  &e.UniqueIDs,
		})

		if e.Resume != nil {
			c.Episodes[len(c.Episodes)-1].Resume.Position = e.Resume.Position
			c.Episodes[len(c.Episodes)-1].Resume.Total = e.Resume.Total
		}
	}
	l.mu.Shows.Unlock()

	return nil
}

// RefreshMovie ...
func RefreshMovie(kodiID, action int) {
	if action == ActionDelete || action == ActionSafeDelete {
		uids := GetUIDsFromKodi(kodiID)
		if uids == nil || uids.TMDB == 0 {
			return
		}

		if action == ActionDelete {
			if _, err := RemoveMovie(uids.TMDB); err != nil {
				log.Warning("Nothing left to remove from Elementum")
			}
		}

		l.mu.Movies.Lock()
		foundIndex := -1
		for i, m := range l.Movies {
			if m.UIDs.Kodi == kodiID {
				foundIndex = i
				break
			}
		}
		if foundIndex != -1 {
			l.Movies = append(l.Movies[:foundIndex], l.Movies[foundIndex+1:]...)
		}
		l.mu.Movies.Unlock()
	}

	RefreshUIDs()
}

// RefreshShow ...
func RefreshShow(kodiID, action int) {
	uids := GetUIDsFromKodi(kodiID)
	if uids == nil || uids.TMDB == 0 {
		return
	}
	PlanShowUpdate(uids.Kodi)

	defer RefreshUIDs()

	if action == ActionDelete || action == ActionSafeDelete {
		if action == ActionDelete {
			id := strconv.Itoa(uids.TMDB)
			if _, err := RemoveShow(id); err != nil {
				log.Warning("Nothing left to remove from Elementum")
			}
		}

		l.mu.Shows.Lock()
		foundIndex := -1
		for i, s := range l.Shows {
			if s.UIDs.Kodi == kodiID {
				foundIndex = i
				break
			}
		}
		if foundIndex != -1 {
			l.Shows = append(l.Shows[:foundIndex], l.Shows[foundIndex+1:]...)
		}
		l.mu.Shows.Unlock()
	} else if action == ActionUpdate {
		deleteDBItem(uids.TMDB, ShowType, false)
	}
}

// RefreshEpisode ...
func RefreshEpisode(kodiID, action int) {
	s, e := GetLibraryEpisode(kodiID)
	if s == nil || e == nil {
		return
	}

	PlanShowUpdate(s.UIDs.Kodi)

	if action != ActionDelete && action != ActionSafeDelete {
		return
	}

	if action == ActionDelete {
		RemoveEpisode(e.UIDs.TMDB, s.UIDs.TMDB, e.Season, e.Episode)
	}

	l.mu.Shows.Lock()
	sIndex := -1
	eIndex := -1
	for i, sh := range l.Shows {
		if sh.ID == s.UIDs.Kodi {
			sIndex = i
			break
		}
	}

	for i, e := range l.Shows[sIndex].Episodes {
		if e.ID == kodiID {
			eIndex = i
			break
		}
	}
	if eIndex != -1 {
		l.Shows[sIndex].Episodes = append(l.Shows[sIndex].Episodes[:eIndex], l.Shows[sIndex].Episodes[eIndex+1:]...)
	}
	l.mu.Shows.Unlock()

	RefreshUIDs()
}

// RefreshUIDs updates unique IDs for each library item
// This collects already saved UIDs for easier access
func RefreshUIDs() error {
	return RefreshUIDsRunner(false)
}

// RefreshUIDsRunner completes RefreshUIDs target
func RefreshUIDsRunner(force bool) error {
	if !force && (l.Running.IsTrakt || l.Running.IsKodi) {
		return nil
	}

	now := time.Now()

	l.mu.UIDs.Lock()
	defer l.mu.UIDs.Unlock()

	playcount.Mu.Lock()
	defer playcount.Mu.Unlock()
	playcount.Watched = []uint64{}
	l.UIDs = []*UniqueIDs{}
	for _, v := range l.WatchedTraktMovies {
		playcount.Watched = append(playcount.Watched, v)
	}
	for _, v := range l.WatchedTraktShows {
		playcount.Watched = append(playcount.Watched, v)
	}

	for _, m := range l.Movies {
		m.UIDs.MediaType = MovieType
		l.UIDs = append(l.UIDs, m.UIDs)

		if m.UIDs.Playcount > 0 {
			playcount.Watched = append(playcount.Watched,
				xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", MovieType, TMDBScraper, m.UIDs.TMDB)),
				xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", MovieType, TraktScraper, m.UIDs.Trakt)),
				xxhash.Sum64String(fmt.Sprintf("%d_%d_%s", MovieType, IMDBScraper, m.UIDs.IMDB)))
		}
	}

	for _, s := range l.Shows {
		s.UIDs.MediaType = ShowType
		l.UIDs = append(l.UIDs, s.UIDs)

		if s.UIDs.Playcount > 0 {
			playcount.Watched = append(playcount.Watched,
				xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", ShowType, TMDBScraper, s.UIDs.TMDB)),
				xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", ShowType, TraktScraper, s.UIDs.Trakt)),
				xxhash.Sum64String(fmt.Sprintf("%d_%d_%d", ShowType, TVDBScraper, s.UIDs.TVDB)))
		}

		for _, e := range s.Seasons {
			e.UIDs.MediaType = SeasonType
			l.UIDs = append(l.UIDs, e.UIDs)

			if e.UIDs.Playcount > 0 {
				playcount.Watched = append(playcount.Watched,
					xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d", SeasonType, TMDBScraper, s.UIDs.TMDB, e.Season)),
					xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d", SeasonType, TraktScraper, s.UIDs.Trakt, e.Season)),
					xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d", SeasonType, TVDBScraper, s.UIDs.TVDB, e.Season)))
			}
		}

		for _, e := range s.Episodes {
			e.UIDs.MediaType = EpisodeType
			l.UIDs = append(l.UIDs, e.UIDs)

			if e.UIDs.Playcount > 0 {
				playcount.Watched = append(playcount.Watched,
					xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d_%d", EpisodeType, TMDBScraper, s.UIDs.TMDB, e.Season, e.Episode)),
					xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d_%d", EpisodeType, TraktScraper, s.UIDs.Trakt, e.Season, e.Episode)),
					xxhash.Sum64String(fmt.Sprintf("%d_%d_%d_%d_%d", EpisodeType, TVDBScraper, s.UIDs.TVDB, e.Season, e.Episode)))
			}
		}
	}

	log.Debugf("UIDs refresh finished in %s", time.Since(now))
	return nil
}

func parseUniqueID(entityType int, i *UniqueIDs, xbmcIDs *xbmc.UniqueIDs, fileName string, entityYear int) {
	i.MediaType = entityType

	convertKodiIDsToLibrary(i, xbmcIDs)

	if i.TMDB != 0 {
		return
	}

	// If this is a strm file then we try to get TMDB id from it
	if len(fileName) > 0 {
		id, err := findTMDBInFile(fileName, xbmcIDs.Unknown)
		if id != 0 {
			i.TMDB = id
			return
		} else if err != nil {
			log.Debugf("Error reading TMDB ID from the file %s: %#v", fileName, err)
		}
	}

	// We should not query for each episode, has no sense,
	// since we need only TVShow ID to be resolved
	if entityType == EpisodeType {
		return
	}

	// If we get here - we have no TMDB, so try to resolve it
	if len(i.IMDB) != 0 {
		i.TMDB = findTMDBIDs(entityType, "imdb_id", i.IMDB)
		if i.TMDB != 0 {
			return
		}
	}
	if i.TVDB != 0 {
		i.TMDB = findTMDBIDs(entityType, "tvdb_id", strconv.Itoa(i.TVDB))
		if i.TMDB != 0 {
			return
		}
	}

	// We don't have any Named IDs, only 'Unknown' so let's try to fetch it
	if xbmcIDs.Unknown != "" {
		localID, _ := strconv.Atoi(xbmcIDs.Unknown)
		if localID == 0 {
			return
		}

		// Try to treat as it is a TMDB id inside of Unknown field
		if entityType == MovieType {
			m := tmdb.GetMovie(localID, config.Get().Language)
			if m != nil {
				dt, err := time.Parse("2006-01-02", m.FirstAirDate)
				if err != nil || dt.Year() == entityYear {
					i.TMDB = m.ID
					return
				}
			}
		} else if entityType == ShowType {
			s := tmdb.GetShow(localID, config.Get().Language)
			if s != nil {
				dt, err := time.Parse("2006-01-02", s.FirstAirDate)
				if err != nil || dt.Year() == entityYear {
					i.TMDB = s.ID
					return
				}
			}

			// If not found, try to search as TVDB id
			id := findTMDBIDsWithYear(ShowType, "tvdb_id", xbmcIDs.Unknown, entityYear)
			if id != 0 {
				i.TMDB = id
				return
			}
		}
	}

	return
}

func convertKodiIDsToLibrary(i *UniqueIDs, xbmcIDs *xbmc.UniqueIDs) {
	if i == nil || xbmcIDs == nil {
		return
	}

	i.Kodi = xbmcIDs.Kodi
	i.IMDB = xbmcIDs.IMDB
	i.TMDB, _ = strconv.Atoi(xbmcIDs.TMDB)
	i.TVDB, _ = strconv.Atoi(xbmcIDs.TVDB)
	i.Trakt, _ = strconv.Atoi(xbmcIDs.Trakt)

	// Checking alternative fields
	// 		TheMovieDB
	if i.TMDB == 0 && len(xbmcIDs.TheMovieDB) > 0 {
		i.TMDB, _ = strconv.Atoi(xbmcIDs.TheMovieDB)
	}

	// 		IMDB
	if len(xbmcIDs.Unknown) > 0 && strings.HasPrefix(xbmcIDs.Unknown, "tt") {
		i.IMDB = xbmcIDs.Unknown
	}
}

func findTMDBInFile(fileName string, pattern string) (id int, err error) {
	if len(fileName) == 0 || !strings.HasSuffix(fileName, ".strm") {
		return
	}

	// Let's cache file search, it's bad to do that, anyway,
	// but we check only .strm files and do that once per 2 weeks
	cacheKey := fmt.Sprintf(cache.LibraryResolveFileKey, fileName)
	if err := cacheStore.Get(cacheKey, &id); err == nil {
		return id, nil
	}
	defer func() {
		if id == 0 {
			log.Debugf("Count not get ID from the file %s with pattern %s", fileName, pattern)
		}
		cacheStore.Set(cacheKey, id, cache.LibraryResolveFileExpire)
	}()

	if _, errStat := os.Stat(fileName); errStat != nil {
		return 0, errStat
	}

	fileContent, errRead := ioutil.ReadFile(fileName)
	if errRead != nil {
		return 0, errRead
	}

	// Dummy check. If Unknown is found in the strm file - we treat it as tmdb id
	if len(pattern) > 1 && bytes.Contains(fileContent, []byte("/"+pattern)) {
		id, _ = strconv.Atoi(pattern)
		return
	}

	// Reading the strm file and passing to a regexp to get TMDB ID
	// This can't be done with episodes, since it has Show ID and not Episode ID
	if matches := resolveRegexp.FindSubmatch(fileContent); len(matches) > 1 {
		id, _ = strconv.Atoi(string(matches[1]))
		return
	}

	return
}

func findTMDBIDsWithYear(entityType int, source string, id string, year int) int {
	results := tmdb.Find(id, source)
	reserveID := 0

	if results != nil {
		if entityType == MovieType && len(results.MovieResults) > 0 {
			for _, e := range results.MovieResults {
				dt, err := time.Parse("2006-01-02", e.FirstAirDate)
				if err != nil || year == 0 || dt.Year() == 0 {
					reserveID = e.ID
					continue
				}
				if dt.Year() == year {
					return e.ID
				}
			}
		} else if entityType == ShowType && len(results.TVResults) > 0 {
			for _, e := range results.TVResults {
				dt, err := time.Parse("2006-01-02", e.FirstAirDate)
				if err != nil || year == 0 || dt.Year() == 0 {
					reserveID = e.ID
					continue
				}
				if dt.Year() == year {
					return e.ID
				}
			}
		} else if entityType == EpisodeType && len(results.TVEpisodeResults) > 0 {
			for _, e := range results.TVEpisodeResults {
				dt, err := time.Parse("2006-01-02", e.FirstAirDate)
				if err != nil || year == 0 || dt.Year() == 0 {
					reserveID = e.ID
					continue
				}
				if dt.Year() == year {
					return e.ID
				}
			}
		}
	}

	return reserveID
}

func findTMDBIDs(entityType int, source string, id string) int {
	results := tmdb.Find(id, source)
	if results != nil {
		if entityType == MovieType && len(results.MovieResults) == 1 && results.MovieResults[0] != nil {
			return results.MovieResults[0].ID
		} else if entityType == ShowType && len(results.TVResults) == 1 && results.TVResults[0] != nil {
			return results.TVResults[0].ID
		} else if entityType == EpisodeType && len(results.TVEpisodeResults) == 1 && results.TVEpisodeResults[0] != nil {
			return results.TVEpisodeResults[0].ID
		}
	}

	return 0
}

func findTraktIDs(entityType int, source int, id string) (ids *trakt.IDs) {
	switch entityType {
	case MovieType:
		var r *trakt.Movie
		if source == TMDBScraper {
			r = trakt.GetMovieByTMDB(id)
		} else if source == TraktScraper {
			r = trakt.GetMovie(id)
		}
		if r != nil && r.IDs != nil {
			ids = r.IDs
		}
	case ShowType:
		var r *trakt.Show
		if source == TMDBScraper {
			r = trakt.GetShowByTMDB(id)
		} else if source == TraktScraper {
			r = trakt.GetShow(id)
		}
		if r != nil && r.IDs != nil {
			ids = r.IDs
		}
	case EpisodeType:
		var r *trakt.Episode
		if source == TMDBScraper {
			r = trakt.GetEpisodeByTMDB(id)
		} else if source == TraktScraper {
			r = trakt.GetEpisodeByID(id)
		}
		if r != nil && r.IDs != nil {
			ids = r.IDs
		}
	}

	return
}

// RefreshLocal checks media directory to save up-to-date strm library
func RefreshLocal() error {
	if l.Running.IsOverall {
		return nil
	}

	l.Pending.IsOverall = false
	l.Running.IsOverall = true
	defer func() {
		l.Running.IsOverall = false
		util.FreeMemoryGC()
	}()

	refreshLocalMovies()
	refreshLocalShows()

	return nil
}

func refreshLocalMovies() {
	if !config.Get().LibraryEnabled {
		return
	}

	moviesLibraryPath := MoviesLibraryPath()
	if _, err := os.Stat(moviesLibraryPath); err != nil {
		return
	}

	begin := time.Now()
	addon := []byte(config.Get().Info.ID)
	files := searchStrm(moviesLibraryPath)
	IDs := []int{}
	for _, f := range files {
		fileContent, err := ioutil.ReadFile(f)
		if err != nil || len(fileContent) == 0 || bytes.Index(fileContent, addon) < 0 {
			continue
		}

		if matches := movieRegexp.FindSubmatch(fileContent); len(matches) > 1 {
			id, _ := strconv.Atoi(string(matches[1]))
			IDs = append(IDs, id)
		}
	}

	if len(IDs) == 0 {
		return
	}

	log.Debugf("Finished updating %d local movies in %s", len(IDs), time.Since(begin))

	return
}

func refreshLocalShows() {
	if !config.Get().LibraryEnabled {
		return
	}

	showsLibraryPath := ShowsLibraryPath()
	if _, err := os.Stat(showsLibraryPath); err != nil {
		return
	}

	begin := time.Now()
	addon := []byte(config.Get().Info.ID)
	files := searchStrm(showsLibraryPath)
	IDs := map[int]bool{}
	for _, f := range files {
		fileContent, err := ioutil.ReadFile(f)
		if err != nil || len(fileContent) == 0 || bytes.Index(fileContent, addon) < 0 {
			continue
		}

		if matches := showRegexp.FindSubmatch(fileContent); len(matches) > 1 {
			showID, _ := strconv.Atoi(string(matches[1]))

			if !wasRemoved(showID, ShowType) {
				IDs[showID] = true
			}
		}
	}

	if len(IDs) == 0 {
		return
	}

	for id := range IDs {
		updateDBItem(id, StateActive, ShowType, id)
	}
	log.Debugf("Finished updating %d local shows in %s", len(IDs), time.Since(begin))

	return
}

func searchStrm(dir string) []string {
	ret := []string{}
	foundDirs := []string{}

	godirwalk.Walk(dir, &godirwalk.Options{
		FollowSymbolicLinks: true,
		Callback: func(osPathname string, de *godirwalk.Dirent) error {
			if !strings.HasSuffix(osPathname, ".strm") {
				return nil
			}

			// Make sure we return only one file per directory, no need to get all of them
			dir := filepath.Dir(osPathname)
			if util.StringSliceContains(foundDirs, dir) {
				return nil
			}

			foundDirs = append(foundDirs, dir)
			ret = append(ret, osPathname)
			return nil
		},
	})

	return ret
}

// MarkKodiRefresh ...
func MarkKodiRefresh() {
	l.Running.IsKodi = true
}

// MarkKodiUpdated ...
func MarkKodiUpdated() {
	isKodiUpdated = true
}

// MarkKodiAdded ...
func MarkKodiAdded() {
	isKodiAdded = true
}

// PlanOverallUpdate ...
func PlanOverallUpdate() {
	l.Pending.IsOverall = true
}

// PlanKodiUpdate ...
func PlanKodiUpdate() {
	l.Pending.IsKodi = true
}

// PlanTraktUpdate ...
func PlanTraktUpdate() {
	l.Pending.IsTrakt = true
}

// PlanMoviesUpdate ...
func PlanMoviesUpdate() {
	l.Pending.IsMovies = true
}

// PlanShowsUpdate ...
func PlanShowsUpdate() {
	l.Pending.IsShows = true
}

// PlanShowUpdate ...
func PlanShowUpdate(showID int) {
	pendingShows[showID] = true
	l.Pending.IsEpisodes = true
}
