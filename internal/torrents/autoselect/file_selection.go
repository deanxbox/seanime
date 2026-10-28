package autoselect

import (
	"context"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/debrid/debrid"
	"seanime/internal/extension"
	hibiketorrent "seanime/internal/extension/hibike/torrent"
	torrentanalyzer "seanime/internal/torrents/analyzer"
	"seanime/internal/util"
	"slices"
	"strconv"

	"github.com/anacrolix/torrent"
	"github.com/samber/lo"
)

const (
	MaxTorrentCandidatesToCheck = 3
	MaxAnalyzedTorrents         = 3
	// MaxFallbackCandidates is how many extra candidates are tried after the top ones failed (auto-swap).
	MaxFallbackCandidates = 6
)

func (s *AutoSelect) selectFile(
	ctx context.Context,
	media *anilist.CompleteAnime,
	episodeNumber int,
	torrents []*hibiketorrent.AnimeTorrent,
	mode SelectionMode,
	torrentClient TorrentClient,
	debridClient debrid.Provider,
) (*Result, error) {
	// Go through the top torrents, then swap to further candidates (preferring other providers) if they all fail
	maxAttempts := MaxAnalyzedTorrents + MaxFallbackCandidates
	remaining := slices.Clone(torrents)
	failedProviders := make(map[string]struct{})
	attempts := 0

	for len(remaining) > 0 && attempts < maxAttempts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// past the top candidates, prefer a torrent from a provider that hasn't failed yet
		pick := 0
		if attempts >= MaxAnalyzedTorrents {
			if i := slices.IndexFunc(remaining, func(c *hibiketorrent.AnimeTorrent) bool {
				_, failed := failedProviders[c.Provider]
				return !failed
			}); i > 0 {
				pick = i
			}
		}
		t := remaining[pick]
		remaining = slices.Delete(remaining, pick, pick+1)

		if attempts >= MaxAnalyzedTorrents {
			s.logger.Info().Str("provider", t.Provider).Msgf("autoselect: Previous candidates failed, swapping to fallback candidate: %s", t.Name)
			s.log(fmt.Sprintf("Previous candidates failed, trying fallback: %s (%s)", t.Name, t.Provider))
		}

		s.logger.Debug().Msgf("autoselect: Checking torrent candidate: %s", t.Name)
		s.log(fmt.Sprintf("Checking torrent candidate: %s", t.Name))
		s.updateCandidateStatus(ctx, t.Name, "analyzing")

		providerExt, ok := s.torrentRepository.GetAnimeProviderExtension(t.Provider)
		if !ok {
			s.logger.Warn().Str("provider", t.Provider).Msg("autoselect: Provider not found")
			s.updateCandidateStatus(ctx, t.Name, "skipped")
			continue
		}

		var res *Result
		var err error

		switch mode {
		case SelectionModeDebrid:
			if debridClient != nil {
				res, err = s.selectFileFromDebrid(ctx, media, episodeNumber, t, providerExt, debridClient)
			} else {
				s.logger.Error().Msg("autoselect: Debrid client is nil but mode is Debrid")
				s.updateCandidateStatus(ctx, t.Name, "skipped")
				continue
			}
		case SelectionModeTorrent:
			if torrentClient != nil {
				res, err = s.selectFileFromTorrentClient(ctx, media, episodeNumber, t, providerExt, torrentClient)
			} else {
				s.logger.Error().Msg("autoselect: Torrent client is nil but mode is Torrent")
				s.updateCandidateStatus(ctx, t.Name, "skipped")
				continue
			}
		}

		if err == nil && res != nil {
			s.updateCandidateStatus(ctx, t.Name, "selected")
			return res, nil
		}

		if err != nil {
			s.logger.Warn().Err(err).Msgf("autoselect: Could not select file for %s", t.Name)
		}
		s.updateCandidateStatus(ctx, t.Name, "skipped")
		failedProviders[t.Provider] = struct{}{}

		// Count the analysis attempt if we actually tried
		attempts++
	}

	return nil, ErrNoFileFound
}

func (s *AutoSelect) selectFileFromTorrentClient(
	ctx context.Context,
	media *anilist.CompleteAnime,
	episodeNumber int,
	t *hibiketorrent.AnimeTorrent,
	providerExt extension.AnimeTorrentProviderExtension,
	client TorrentClient,
) (res *Result, err error) {
	defer util.HandlePanicInModuleWithError("autoselect/selectFileFromTorrentClient", &err)

	s.logger.Trace().Msgf("autoselect: Getting torrent magnet")
	magnet, err := providerExt.GetProvider().GetTorrentMagnetLink(t)
	if err != nil {
		s.logger.Warn().Err(err).Msgf("autoselect: Error scraping magnet link for %s", t.Link)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.logger.Debug().Msgf("autoselect: Adding torrent %s from magnet", t.Link)

	addedTorrent, err := client.AddTorrent(ctx, magnet)
	if err != nil {
		s.logger.Warn().Err(err).Msgf("autoselect: Error adding torrent %s", t.Link)
		return nil, err
	}

	// Override magnet link
	t.MagnetLink = magnet

	// Only one file, use it
	if len(addedTorrent.Files()) == 1 {
		tFile := addedTorrent.Files()[0]
		addedTorrent.DownloadAll()
		return &Result{
			Torrent:         addedTorrent,
			File:            tFile,
			OriginalTorrent: t,
		}, nil
	}

	// get file paths
	filepaths := lo.Map(addedTorrent.Files(), func(f *torrent.File, _ int) string {
		return f.DisplayPath()
	})

	if len(filepaths) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	// Remove the torrent
	cancel := func() {
		go func() {
			_ = client.RemoveTorrent(addedTorrent.InfoHash().AsString())
		}()
	}

	analyzer := torrentanalyzer.NewAnalyzer(&torrentanalyzer.NewAnalyzerOptions{
		Logger:              s.logger,
		Filepaths:           filepaths,
		Media:               media,
		PlatformRef:         s.platform,
		MetadataProviderRef: s.metadataProvider,
		ForceMatch:          true,
	})

	analysis, err := analyzer.AnalyzeTorrentFiles()
	if err != nil {
		cancel()
		return nil, err
	}

	analysisFile, found := analysis.GetFileByAniDBEpisode(strconv.Itoa(episodeNumber))
	if !found {
		cancel()
		return nil, fmt.Errorf("episode not found")
	}

	// Download the file and unselect the rest
	for i, f := range addedTorrent.Files() {
		if i != analysisFile.GetIndex() {
			f.SetPriority(torrent.PiecePriorityNone)
		}
	}
	tFile := addedTorrent.Files()[analysisFile.GetIndex()]
	tFile.Download()

	s.log(fmt.Sprintf("Selected file: %s", tFile.DisplayPath()))

	return &Result{
		Torrent:         addedTorrent,
		File:            tFile,
		AnalysisFile:    analysisFile,
		OriginalTorrent: t,
	}, nil
}

func (s *AutoSelect) selectFileFromDebrid(
	ctx context.Context,
	media *anilist.CompleteAnime,
	episodeNumber int,
	t *hibiketorrent.AnimeTorrent,
	providerExt extension.AnimeTorrentProviderExtension,
	client debrid.Provider,
) (*Result, error) {

	s.logger.Trace().Msgf("autoselect: Getting torrent magnet")
	magnet, err := providerExt.GetProvider().GetTorrentMagnetLink(t)
	if err != nil {
		s.logger.Warn().Err(err).Msgf("autoselect: Error scraping magnet link for %s", t.Link)
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Override magnet link
	t.MagnetLink = magnet

	s.logger.Debug().Msgf("autoselect: Checking debrid info for %s", t.Link)

	info, err := client.GetTorrentInfo(debrid.GetTorrentInfoOptions{
		MagnetLink: magnet,
		InfoHash:   t.InfoHash,
	})
	if err != nil {
		s.logger.Warn().Err(err).Msgf("autoselect: Error getting debrid info %s", t.Link)
		return nil, err
	}

	filepaths := lo.Map(info.Files, func(f *debrid.TorrentItemFile, _ int) string {
		return f.Path
	})

	if len(filepaths) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	analyzer := torrentanalyzer.NewAnalyzer(&torrentanalyzer.NewAnalyzerOptions{
		Logger:              s.logger,
		Filepaths:           filepaths,
		Media:               media,
		PlatformRef:         s.platform,
		MetadataProviderRef: s.metadataProvider,
		ForceMatch:          true,
	})

	analysis, err := analyzer.AnalyzeTorrentFiles()
	if err != nil {
		return nil, err
	}

	analysisFile, found := analysis.GetFileByAniDBEpisode(strconv.Itoa(episodeNumber))
	if !found {
		return nil, fmt.Errorf("episode not found")
	}

	tFile := info.Files[analysisFile.GetIndex()]
	s.logger.Debug().Msgf("autoselect: Selected debrid file %s", tFile.Name)
	s.log(fmt.Sprintf("Selected debrid file: %s", tFile.Name))

	return &Result{
		DebridTorrent:   info,
		DebridFileID:    tFile.ID,
		AnalysisFile:    analysisFile,
		OriginalTorrent: t,
	}, nil
}
