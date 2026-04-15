import AppKit
import FileProviderUI
import RoughdashHelperCore

final class RoughdashFileProviderUIViewController: NSViewController, FPUIActionExtensionViewController {
    private let label = NSTextField(labelWithString: "Roughdash needs your attention.")

    override func loadView() {
        view = NSView(frame: NSRect(x: 0, y: 0, width: 420, height: 180))
        label.translatesAutoresizingMaskIntoConstraints = false
        label.alignment = .center
        view.addSubview(label)
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 24),
            label.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -24),
            label.centerYAnchor.constraint(equalTo: view.centerYAnchor)
        ])
    }

    func prepare(forAction actionIdentifier: String, itemIdentifiers: [NSFileProviderItemIdentifier]) {
        switch actionIdentifier {
        case "com.roughdash.keep-downloaded":
            label.stringValue = "Roughdash will keep the selected item downloaded."
        case "com.roughdash.remove-download":
            label.stringValue = "Roughdash will remove local bytes if there are no unsynced edits."
        case "com.roughdash.delete-everywhere":
            label.stringValue = "Delete Everywhere removes the item from TrueNAS and all synced Macs."
        default:
            label.stringValue = "Roughdash needs your attention."
        }
    }
}
