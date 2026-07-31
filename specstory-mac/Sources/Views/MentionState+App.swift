import Foundation
import SpecStoryKit

/// Bridges app-level types into the pure candidate computation: projects come
/// from the synced cloud project list, agents from the provider registry's
/// display names (mirroring the web client's canonical identity list).
extension MentionState {
    func candidatesFromApp(cloudProjects: [CloudProject]) -> [MentionItem] {
        candidates(
            projects: cloudProjects.map { (id: $0.id, name: $0.name) },
            agents: Provider.allCases.map(\.displayName)
        )
    }
}
