import React from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it, vi } from "vitest"
import { Anime_Episode } from "@/api/generated/types"
import { EpisodeCard } from "./episode-card"

vi.mock("@/app/(main)/_features/context-menu/sea-context-menu", () => ({
    SeaContextMenu: ({ children }: React.PropsWithChildren) => children,
}))
vi.mock("@/components/ui/context-menu", () => ({
    ContextMenuTrigger: ({ children }: React.PropsWithChildren) => children,
    ContextMenuGroup: () => null,
    ContextMenuItem: () => null,
    ContextMenuLabel: () => null,
}))
vi.mock("@/app/(main)/_hooks/use-server-status", () => ({ useServerStatus: () => ({}) }))
vi.mock("@/lib/theme/theme-hooks", () => ({ useThemeSettings: () => ({}) }))
vi.mock("@/lib/navigation", () => ({ useRouter: () => ({}), usePathname: () => "/entry" }))
vi.mock("@/app/(main)/_features/media/_containers/media-preview-modal", () => ({
    useMediaPreviewModal: () => ({}),
}))
vi.mock("@/app/(main)/_features/playlists/lib/playlist-editor-manager", () => ({
    usePlaylistEditorManager: () => ({}),
}))
vi.mock("@/lib/theme/anime-spoilers", () => ({
    useEpisodeSpoilerState: () => ({}),
    getSpoilerFreeAnimeImage: () => undefined,
}))
vi.mock("@/app/(main)/_features/anime/_components/episode-card-image", () => ({ EpisodeCardImage: () => null }))
vi.mock("@/components/shared/sea-image", () => ({ SeaImage: () => null }))
vi.mock("@/lib/server/assets", () => ({ getImageUrl: (url: string) => url }))
vi.mock("@/app/(main)/_features/custom-ui/item-bottom-gradients", () => ({ EpisodeItemBottomGradient: () => null }))
vi.mock("@/app/(main)/_features/media/_components/media-entry-card-components", () => ({ MediaEntryCardAdultVeil: () => null }))
vi.mock("../../plugin/actions/plugin-actions", () => ({ PluginEpisodeCardContextMenuItems: () => null }))

function renderCard(isFiller?: boolean, badge?: React.ReactNode) {
    const episode = { episodeMetadata: { isFiller } } as Anime_Episode
    return renderToStaticMarkup(<EpisodeCard title="Episode 82" episode={episode} badge={badge} />)
}

describe("episode card filler badge", () => {
    it("renders filler metadata using the existing orange badge style", () => {
        const html = renderCard(true)
        expect(html).toContain("data-episode-card-filler-badge")
        expect(html).toContain(">Filler</span>")
        expect(html).toContain("bg-orange-800")
    })

    it.each([false, undefined])("does not label non-filler or unknown episodes (%s)", isFiller => {
        expect(renderCard(isFiller)).not.toContain("data-episode-card-filler-badge")
    })

    it("accepts an episode without metadata and cards without an episode", () => {
        for (const episode of [{} as Anime_Episode, undefined]) {
            const html = renderToStaticMarkup(<EpisodeCard title="Episode" episode={episode} />)
            expect(html).not.toContain("data-episode-card-filler-badge")
        }
    })

    it.each([true, false])("preserves custom badges alongside filler status (%s)", isFiller => {
        const html = renderCard(isFiller, <span>Available</span>)
        expect(html).toContain(">Available</span>")
        expect(html.includes("data-episode-card-filler-badge")).toBe(isFiller)
    })
})
