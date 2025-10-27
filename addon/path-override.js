// ======================================================================
// SpecStory Custom Path Override - Enhanced Backup Mode
// Unified history + Project-based categorization
// ======================================================================
// 自动备份 .specstory/history 到自定义路径（支持扩展目录和项目目录）
// 同时支持项目分类备份功能

const vscode = require('vscode');
const fs = require('fs');
const path = require('path');

// Import project-based backup module (separate file for modularity)
const projectBackup = require('./project-backup.js');

// ----------------------------------------------------------------------
// LEGACY BACKUP MODE (kept for backward compatibility)
// Simple backup to single directory (no project categorization)
// ----------------------------------------------------------------------

/**
 * Backup a single file to custom path (legacy mode)
 * This is used when project categorization is disabled
 * @param {string} srcPath - Source file path
 * @param {string} customBasePath - Destination base path
 * @returns {boolean} - Success status
 */
function backupFile(srcPath, customBasePath) {
    try {
        if (!fs.existsSync(srcPath)) return;
        
        // Read source file
        const content = fs.readFileSync(srcPath);
        
        // Build destination path (keep same filename)
        const fileName = path.basename(srcPath);
        const destPath = path.join(customBasePath, fileName);
        
        // Ensure destination directory exists
        if (!fs.existsSync(customBasePath)) {
            fs.mkdirSync(customBasePath, { recursive: true });
        }
        
        // Write backup file
        fs.writeFileSync(destPath, content);
        console.log('[SpecStory Backup] Backed up:', fileName);
        
        return true;
    } catch (error) {
        console.error('[SpecStory Backup] File backup failed:', error);
        return false;
    }
}

// ----------------------------------------------------------------------
// DIRECTORY BACKUP DISPATCHER
// Routes to either legacy or project-based backup based on config
// ----------------------------------------------------------------------

/**
 * Backup entire history directory
 * @param {string} historyPath - Source history directory
 * @param {string} customPath - Destination base path
 * @param {string} projectName - Project name (optional, for project mode)
 * @param {boolean} useProjectMode - Whether to use project categorization
 * @returns {number} - Number of files backed up
 */
function backupHistoryDirectory(historyPath, customPath, projectName = null, useProjectMode = false) {
    try {
        if (!fs.existsSync(historyPath)) {
            console.log('[SpecStory Backup] History directory not found:', historyPath);
            return 0;
        }
        
        // Ensure custom path exists
        if (!fs.existsSync(customPath)) {
            fs.mkdirSync(customPath, { recursive: true });
            console.log('[SpecStory Backup] Created backup directory:', customPath);
        }
        
        // ====================================================
        // PROJECT MODE: Use project-based backup module
        // ====================================================
        if (useProjectMode && projectName) {
            const stats = projectBackup.backupProjectDirectory(
                historyPath, 
                customPath, 
                projectName
            );
            return stats.success;
        }
        
        // ====================================================
        // LEGACY MODE: Simple flat backup
        // ====================================================
        const files = fs.readdirSync(historyPath);
        let backedUpCount = 0;
        
        files.forEach(file => {
            if (file.endsWith('.md')) {
                const srcPath = path.join(historyPath, file);
                if (backupFile(srcPath, customPath)) {
                    backedUpCount++;
                }
            }
        });
        
        if (backedUpCount > 0) {
            console.log(`[SpecStory Backup] Backed up ${backedUpCount} files to ${customPath}`);
        }
        
        return backedUpCount;
        
    } catch (error) {
        console.error('[SpecStory Backup] Directory backup failed:', error);
        return 0;
    }
}

// ----------------------------------------------------------------------
// FILE WATCHING DISPATCHER  
// Routes to either legacy or project-based watcher
// ----------------------------------------------------------------------

/**
 * Watch directory for changes and auto-backup new files
 * @param {string} historyPath - Directory to watch
 * @param {string} customPath - Destination base path
 * @param {string} label - Label for logging
 * @param {string} projectName - Project name (optional, for project mode)
 * @param {boolean} useProjectMode - Whether to use project categorization
 * @returns {fs.FSWatcher|null} - Watcher instance
 */
function watchDirectory(historyPath, customPath, label, projectName = null, useProjectMode = false) {
    try {
        if (!fs.existsSync(historyPath)) {
            console.log(`[SpecStory Backup] ${label}: Waiting for directory creation...`);
            return null;
        }
        
        console.log(`[SpecStory Backup] ${label}: Started watching ${historyPath}`);
        
        // ====================================================
        // PROJECT MODE: Use project-based watcher
        // ====================================================
        if (useProjectMode && projectName) {
            return projectBackup.watchProjectDirectory(
                historyPath,
                customPath,
                projectName,
                label
            );
        }
        
        // ====================================================
        // LEGACY MODE: Simple watcher
        // ====================================================
        const watcher = fs.watch(historyPath, { recursive: false }, (eventType, filename) => {
            if (!filename || !filename.endsWith('.md')) return;
            
            const srcPath = path.join(historyPath, filename);
            
            // Delay to ensure file write is complete
            setTimeout(() => {
                if (fs.existsSync(srcPath)) {
                    backupFile(srcPath, customPath);
                }
            }, 500);
        });
        
        return watcher;
        
    } catch (error) {
        console.error(`[SpecStory Backup] ${label}: Watch failed:`, error);
        return null;
    }
}

// ----------------------------------------------------------------------
// HISTORY PATH DISCOVERY
// Find all .specstory/history directories with project context
// ----------------------------------------------------------------------

/**
 * Get all possible history directories with project information
 * @returns {Array<object>} - Array of {path, label, projectName, workspacePath}
 */
function getAllHistoryPaths() {
    const paths = [];
    
    try {
        // ====================================================
        // 1. WORKSPACE FOLDERS
        // Detect history in all workspace folders (project mode)
        // ====================================================
        const workspaceFolders = vscode.workspace.workspaceFolders;
        if (workspaceFolders && workspaceFolders.length > 0) {
            workspaceFolders.forEach(folder => {
                const historyPath = path.join(folder.uri.fsPath, '.specstory', 'history');
                const projectName = projectBackup.extractProjectName(folder.uri.fsPath);
                
                paths.push({
                    path: historyPath,
                    label: `Workspace: ${folder.name}`,
                    projectName: projectName,
                    workspacePath: folder.uri.fsPath,
                    isProject: true  // Mark as project-based
                });
            });
        }
        
        // ====================================================
        // 2. EXTENSION DIRECTORY
        // Detect if user is working within extension directory
        // ====================================================
        const activeEditor = vscode.window.activeTextEditor;
        if (activeEditor) {
            const activeFilePath = activeEditor.document.uri.fsPath;
            if (activeFilePath.includes('.cursor\\extensions\\specstory')) {
                // Extract extension root directory
                const match = activeFilePath.match(/(.*?\.cursor\\extensions\\specstory[^\\]*)/);
                if (match) {
                    const extRoot = match[1];
                    const historyPath = path.join(extRoot, '.specstory', 'history');
                    
                    // Avoid duplicates
                    if (!paths.some(p => p.path === historyPath)) {
                        const projectName = projectBackup.extractProjectName(extRoot);
                        
                        paths.push({
                            path: historyPath,
                            label: 'Extension Directory',
                            projectName: projectName,
                            workspacePath: extRoot,
                            isProject: true
                        });
                    }
                }
            }
        }
        
    } catch (error) {
        console.error('[SpecStory Backup] Failed to get history paths:', error);
    }
    
    return paths;
}

// ======================================================================
// MAIN INITIALIZATION
// Set up backup system with project categorization
// ======================================================================

/**
 * Initialize backup functionality
 * Reads config, discovers projects, sets up watchers
 */
function initBackup() {
    try {
        // ====================================================
        // READ CONFIGURATION
        // ====================================================
        const config = vscode.workspace.getConfiguration('specstory');
        const customPathEnabled = config.get('customPath.enabled', false);
        const customPath = config.get('customPath.location', '');
        const useProjectMode = config.get('customPath.projectMode', true); // Default: enable project mode
        const cleanupOnExit = config.get('customPath.cleanupOnExit', false); // Default: disabled
        
        console.log('[SpecStory Backup] Configuration check:', {
            enabled: customPathEnabled,
            path: customPath,
            projectMode: useProjectMode,
            cleanupOnExit
        });
        
        if (!customPathEnabled || !customPath || customPath.trim() === '') {
            console.log('[SpecStory Backup] Custom path not enabled');
            return;
        }
        
        // ====================================================
        // DISCOVER HISTORY DIRECTORIES
        // ====================================================
        const historyPaths = getAllHistoryPaths();
        
        if (historyPaths.length === 0) {
            console.log('[SpecStory Backup] No history directories found');
            return;
        }
        
        console.log('[SpecStory Backup] Found history directories:', historyPaths.length);
        console.log('[SpecStory Backup] Project mode:', useProjectMode ? 'ENABLED' : 'DISABLED');
        
        // ====================================================
        // INITIAL BACKUP + START WATCHING
        // ====================================================
        let totalBackedUp = 0;
        const watchers = [];
        
        historyPaths.forEach(({ path: historyPath, label, projectName, isProject }) => {
            console.log(`[SpecStory Backup] ${label}:`, historyPath);
            console.log(`[SpecStory Backup] Project name: ${projectName}`);
            
            // Backup existing files
            const count = backupHistoryDirectory(
                historyPath, 
                customPath, 
                projectName,
                useProjectMode && isProject
            );
            totalBackedUp += count;
            
            // Start watching for new files
            const watcher = watchDirectory(
                historyPath, 
                customPath, 
                label,
                projectName,
                useProjectMode && isProject
            );
            
            if (watcher) {
                watcher.historyPath = historyPath;
                watcher.projectName = projectName;
                watchers.push(watcher);
            }
        });
        
        if (totalBackedUp > 0) {
            console.log(`[SpecStory Backup] Initial backup complete: ${totalBackedUp} files → ${customPath}`);
        }

        // ====================================================
        // CLEANUP ON EXIT (optional): ensure backup, then delete local .specstory
        // Trigger on window unload/dispose
        // ====================================================
        if (cleanupOnExit) {
            const performCleanup = () => {
                try {
                    const finalPaths = getAllHistoryPaths();
                    finalPaths.forEach(({ path: historyPath, projectName, isProject }) => {
                        if (!isProject) return;
                        projectBackup.ensureProjectBackupFromHistory(historyPath, customPath, projectName);
                        projectBackup.deleteLocalSpecStoryByHistoryPath(historyPath);
                    });
                } catch (err) {
                    console.error('[SpecStory Backup] Cleanup on exit failed:', err);
                }
            };

            try {
                // Register process-based exit hooks for reliability
                process.on('beforeExit', performCleanup);
                process.on('exit', performCleanup);
                process.on('SIGINT', () => { performCleanup(); process.exit(0); });
                process.on('SIGTERM', () => { performCleanup(); process.exit(0); });
            } catch (e) {
                console.error('[SpecStory Backup] Failed to register process exit handlers:', e);
            }
        }
        
        // ====================================================
        // PERIODIC DIRECTORY DISCOVERY
        // Check for new projects every 30 seconds
        // ====================================================
        setInterval(() => {
            const newPaths = getAllHistoryPaths();
            newPaths.forEach(({ path: historyPath, label, projectName, isProject }) => {
                // Check if this path is already being watched
                if (!watchers.some(w => w && w.historyPath === historyPath)) {
                    console.log(`[SpecStory Backup] New directory discovered: ${label}`);
                    
                    // Backup existing files
                    backupHistoryDirectory(
                        historyPath, 
                        customPath, 
                        projectName,
                        useProjectMode && isProject
                    );
                    
                    // Start watching
                    const watcher = watchDirectory(
                        historyPath, 
                        customPath, 
                        label,
                        projectName,
                        useProjectMode && isProject
                    );
                    
                    if (watcher) {
                        watcher.historyPath = historyPath;
                        watcher.projectName = projectName;
                        watchers.push(watcher);
                    }
                }
            });
        }, 30000);
        
    } catch (error) {
        console.error('[SpecStory Backup] Initialization failed:', error);
    }
}

// ======================================================================
// MODULE ENTRY POINT
// This function is called by extension.js
// ======================================================================

/**
 * Entry point called by extension.js
 * Starts backup system with delay to ensure VS Code API is ready
 * @returns {null} - Always returns null to let extension use default path
 */
function getCustomHistoryUri() {
    // Delay startup to ensure VS Code API is fully initialized
    setTimeout(() => {
        initBackup();
    }, 1000);
    
    // Return null to let extension continue using default path
    // (We backup in parallel, not replace the original location)
    return null;
}

module.exports = { getCustomHistoryUri };
console.log('[SpecStory Backup] Backup module loaded (with project categorization support)');
