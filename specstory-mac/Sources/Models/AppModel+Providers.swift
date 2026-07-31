import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Provider management

    func refreshProviderStatuses() async {
        guard let binaryURL else { return }
        let muted = mutedProviders

        if providerStatuses.isEmpty {
            providerStatuses = Provider.allCases.map {
                ProviderStatus(provider: $0, healthy: nil, notificationsOn: !muted.contains($0.rawValue))
            }
        }

        for provider in Provider.allCases {
            let result = try? await CLIRunner.runDetailed(
                binary: binaryURL,
                arguments: ["check", "--providers", provider.rawValue, "--silent"],
                workingDirectory: NSHomeDirectory(), environment: [:], timeout: 30
            )
            let healthy = result.map { $0.exitCode == 0 }
            if let index = providerStatuses.firstIndex(where: { $0.provider == provider }) {
                providerStatuses[index].healthy = healthy
            }
        }
    }

    private var mutedProviders: Set<String> {
        get { Set(UserDefaults.standard.stringArray(forKey: "mutedProviders") ?? []) }
        set { UserDefaults.standard.set(Array(newValue).sorted(), forKey: "mutedProviders") }
    }

    func providerNotificationsOn(_ provider: Provider) -> Bool {
        !mutedProviders.contains(provider.rawValue)
    }

    func setProviderNotifications(_ provider: Provider, on: Bool) {
        var muted = mutedProviders
        if on {
            muted.remove(provider.rawValue)
        } else {
            muted.insert(provider.rawValue)
        }
        mutedProviders = muted
        if let index = providerStatuses.firstIndex(where: { $0.provider == provider }) {
            providerStatuses[index].notificationsOn = on
        }
    }
}
