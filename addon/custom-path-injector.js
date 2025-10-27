// SpecStory Custom Path Feature Injector
// This code hooks into the extension to enable custom storage path functionality

(function() {
    'use strict';
    
    console.log('[SpecStory Custom Path] Injector loaded');
    
    // Wait for VS Code API to be available
    const vscode = require('vscode');
    
    // Store original PathsService methods
    let originalGetSpecStoryHistoryUri = null;
    let pathsServiceInstance = null;
    
    // Hook function to intercept getSpecStoryHistoryUri
    function hookPathsService() {
        try {
            // Get configuration
            const config = vscode.workspace.getConfiguration('specstory');
            const customPathEnabled = config.get('customPath.enabled', false);
            const customPath = config.get('customPath.location', '');
            
            console.log('[SpecStory Custom Path] Config:', { customPathEnabled, customPath });
            
            if (customPathEnabled && customPath && customPath.trim() !== '') {
                // Create custom URI from path
                const fs = require('fs');
                const path = require('path');
                
                // Ensure directory exists
                try {
                    if (!fs.existsSync(customPath)) {
                        fs.mkdirSync(customPath, { recursive: true });
                        console.log('[SpecStory Custom Path] Created directory:', customPath);
                    }
                    
                    // Return custom path URI
                    const customUri = vscode.Uri.file(customPath);
                    console.log('[SpecStory Custom Path] Using custom path:', customUri.fsPath);
                    return customUri;
                } catch (error) {
                    console.error('[SpecStory Custom Path] Error creating custom directory:', error);
                }
            }
            
            // Return original behavior if custom path not enabled
            return null;
        } catch (error) {
            console.error('[SpecStory Custom Path] Hook error:', error);
            return null;
        }
    }
    
    // Inject hook on extension activation
    setTimeout(() => {
        console.log('[SpecStory Custom Path] Attempting to inject hooks...');
        
        // Monitor configuration changes
        vscode.workspace.onDidChangeConfiguration((e) => {
            if (e.affectsConfiguration('specstory.customPath')) {
                console.log('[SpecStory Custom Path] Configuration changed, reloading...');
                vscode.window.showInformationMessage(
                    'SpecStory custom path configuration changed. Please reload the window for changes to take effect.',
                    'Reload Window'
                ).then((selection) => {
                    if (selection === 'Reload Window') {
                        vscode.commands.executeCommand('workbench.action.reloadWindow');
                    }
                });
            }
        });
        
        console.log('[SpecStory Custom Path] Hooks installed successfully');
    }, 1000);
    
})();


