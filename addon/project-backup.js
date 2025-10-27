// ======================================================================
// SpecStory Project-Based Backup Module
// Organizes backups by project while keeping a unified history
// ======================================================================

const fs = require('fs');
const path = require('path');

// ----------------------------------------------------------------------
// PROJECT NAME EXTRACTION
// Extract project name from workspace path
// ----------------------------------------------------------------------

/**
 * Extract a clean project name from a file path
 * @param {string} workspacePath - Full path to workspace folder
 * @returns {string} - Cleaned project name or "Unknown"
 */
function extractProjectName(workspacePath) {
    try {
        // Get the last directory name from the path
        const projectName = path.basename(workspacePath);
        
        // Clean up special characters and make it safe for directory names
        const cleanName = projectName
            .replace(/[\\/:*?"<>|]/g, '-')  // Replace invalid chars
            .replace(/\s+/g, '_')            // Replace spaces with underscores
            .trim();
        
        return cleanName || 'Unknown';
        
    } catch (error) {
        console.error('[Project Backup] Failed to extract project name:', error);
        return 'Unknown';
    }
}

/**
 * Create a project abbreviation for filename prefix
 * @param {string} projectName - Full project name
 * @returns {string} - Abbreviated name (max 15 chars)
 */
function createProjectAbbreviation(projectName) {
    if (!projectName || projectName === 'Unknown') {
        return 'UNK';
    }
    
    // If name is short enough, use it directly
    if (projectName.length <= 15) {
        return projectName;
    }
    
    // Take first 12 chars + "..."
    return projectName.substring(0, 12) + '...';
}

// ----------------------------------------------------------------------
// FILE BACKUP OPERATIONS
// Handle dual-location backup (history + project folder)
// ----------------------------------------------------------------------

/**
 * Backup a single file to both unified history and project-specific folder
 * @param {string} srcPath - Source file path
 * @param {string} customBasePath - Base backup path (e.g., D:/SpecStory)
 * @param {string} projectName - Project name for categorization
 * @returns {object} - Result with success status and paths
 */
function backupFileToProject(srcPath, customBasePath, projectName) {
    const result = {
        success: false,
        historyPath: null,
        projectPath: null,
        error: null
    };
    
    try {
        if (!fs.existsSync(srcPath)) {
            result.error = 'Source file not found';
            return result;
        }
        
        // Read source file content
        const content = fs.readFileSync(srcPath);
        const originalFileName = path.basename(srcPath);
        
        // ------------------------------------------------------
        // BACKUP #1: Unified history folder (no project prefix)
        // D:/SpecStory/history/filename.md
        // ------------------------------------------------------
        const historyDir = path.join(customBasePath, 'history');
        if (!fs.existsSync(historyDir)) {
            fs.mkdirSync(historyDir, { recursive: true });
        }
        
        // Use original filename (no project prefix)
        const historyPath = path.join(historyDir, originalFileName);
        
        fs.writeFileSync(historyPath, content);
        result.historyPath = historyPath;
        
        // ------------------------------------------------------
        // BACKUP #2: Project-specific folder
        // D:/SpecStory/ProjectName/filename.md
        // ------------------------------------------------------
        const projectDir = path.join(customBasePath, projectName);
        if (!fs.existsSync(projectDir)) {
            fs.mkdirSync(projectDir, { recursive: true });
        }
        
        const projectPath = path.join(projectDir, originalFileName);
        fs.writeFileSync(projectPath, content);
        result.projectPath = projectPath;
        
        result.success = true;
        console.log(`[Project Backup] ✓ Saved to history: ${originalFileName}`);
        console.log(`[Project Backup] ✓ Saved to project: ${projectName}/${originalFileName}`);
        
        return result;
        
    } catch (error) {
        result.error = error.message;
        console.error('[Project Backup] Backup failed:', error);
        return result;
    }
}

// ----------------------------------------------------------------------
// INDEX FILE GENERATION
// Generate index/catalog file for each directory
// ----------------------------------------------------------------------

/**
 * Generate an index file (INDEX.md) listing all files in a directory
 * @param {string} dirPath - Directory path to generate index for
 * @param {string} title - Title for the index file
 * @param {string} projectName - Project name (optional, for project folders)
 * @returns {boolean} - Success status
 */
function generateIndexFile(dirPath, title, projectName = null) {
    try {
        if (!fs.existsSync(dirPath)) {
            return false;
        }
        
        // Get all .md files (excluding INDEX.md itself)
        const files = fs.readdirSync(dirPath)
            .filter(f => f.endsWith('.md') && f !== 'INDEX.md')
            .sort()
            .reverse(); // Newest first
        
        if (files.length === 0) {
            return false;
        }
        
        // Build index content
        let content = `# ${title}\n\n`;
        content += `**总文件数：** ${files.length}\n`;
        content += `**最后更新：** ${new Date().toLocaleString('zh-CN')}\n\n`;
        content += `---\n\n`;
        
        if (projectName) {
            content += `**项目名称：** ${projectName}\n\n`;
        }
        
        content += `## 📋 文件列表\n\n`;
        
        files.forEach((file, index) => {
            // Extract date from filename (e.g., 2025-10-26_01-23Z)
            const match = file.match(/(\d{4}-\d{2}-\d{2})_(\d{2}-\d{2}Z)/);
            const date = match ? `${match[1]} ${match[2]}` : '未知日期';
            
            content += `${index + 1}. [${file}](./${encodeURIComponent(file)}) - ${date}\n`;
        });
        
        content += `\n---\n\n`;
        content += `*此目录由 SpecStory 项目分类备份功能自动生成*\n`;
        
        // Write index file
        const indexPath = path.join(dirPath, 'INDEX.md');
        fs.writeFileSync(indexPath, content, 'utf8');
        
        console.log(`[Project Backup] ✓ Generated index: ${path.basename(dirPath)}/INDEX.md (${files.length} files)`);
        
        return true;
        
    } catch (error) {
        console.error('[Project Backup] Failed to generate index:', error);
        return false;
    }
}

/**
 * Update all index files (history + all project folders)
 * @param {string} customBasePath - Base backup path
 * @returns {number} - Number of indexes generated
 */
function updateAllIndexes(customBasePath) {
    try {
        if (!fs.existsSync(customBasePath)) {
            return 0;
        }
        
        let count = 0;
        
        // Generate index for history folder
        const historyDir = path.join(customBasePath, 'history');
        if (fs.existsSync(historyDir)) {
            if (generateIndexFile(historyDir, 'SpecStory 所有项目对话总目录')) {
                count++;
            }
        }
        
        // Generate index for each project folder
        const items = fs.readdirSync(customBasePath);
        items.forEach(item => {
            const itemPath = path.join(customBasePath, item);
            
            // Skip history folder and non-directories
            if (item === 'history' || !fs.statSync(itemPath).isDirectory()) {
                return;
            }
            
            // Generate index for project folder
            if (generateIndexFile(itemPath, `${item} 项目对话目录`, item)) {
                count++;
            }
        });
        
        console.log(`[Project Backup] Generated ${count} index file(s)`);
        
        return count;
        
    } catch (error) {
        console.error('[Project Backup] Failed to update indexes:', error);
        return 0;
    }
}

// ----------------------------------------------------------------------
// DIRECTORY BACKUP OPERATIONS
// Batch backup of all files in a directory
// ----------------------------------------------------------------------

/**
 * Backup all .md files from a directory to both locations
 * @param {string} historyPath - Source history directory
 * @param {string} customBasePath - Base backup path
 * @param {string} projectName - Project name
 * @returns {object} - Statistics about the backup operation
 */
function backupProjectDirectory(historyPath, customBasePath, projectName) {
    const stats = {
        total: 0,
        success: 0,
        failed: 0,
        projectName: projectName
    };
    
    try {
        if (!fs.existsSync(historyPath)) {
            console.log(`[Project Backup] Directory not found: ${historyPath}`);
            return stats;
        }
        
        // Get all .md files
        const files = fs.readdirSync(historyPath);
        const mdFiles = files.filter(file => file.endsWith('.md'));
        
        stats.total = mdFiles.length;
        
        if (mdFiles.length === 0) {
            console.log(`[Project Backup] No .md files found in ${historyPath}`);
            return stats;
        }
        
        console.log(`[Project Backup] Processing ${mdFiles.length} files for project: ${projectName}`);
        
        // Backup each file
        mdFiles.forEach(file => {
            const srcPath = path.join(historyPath, file);
            const result = backupFileToProject(srcPath, customBasePath, projectName);
            
            if (result.success) {
                stats.success++;
            } else {
                stats.failed++;
            }
        });
        
        console.log(`[Project Backup] Complete: ${stats.success}/${stats.total} files backed up for ${projectName}`);
        
        // Generate index files after backup
        updateAllIndexes(customBasePath);
        
        return stats;
        
    } catch (error) {
        console.error('[Project Backup] Directory backup failed:', error);
        return stats;
    }
}

// ----------------------------------------------------------------------
// FILE WATCHING & AUTO-BACKUP
// Monitor directory and auto-backup new files
// ----------------------------------------------------------------------

/**
 * Watch a directory and auto-backup new files to both locations
 * @param {string} historyPath - Directory to watch
 * @param {string} customBasePath - Base backup path
 * @param {string} projectName - Project name
 * @param {string} label - Label for logging
 * @returns {fs.FSWatcher|null} - File system watcher instance
 */
function watchProjectDirectory(historyPath, customBasePath, projectName, label) {
    try {
        if (!fs.existsSync(historyPath)) {
            console.log(`[Project Backup] ${label}: Directory not ready, skipping watch`);
            return null;
        }
        
        console.log(`[Project Backup] ${label}: Watching for new files...`);
        
        const watcher = fs.watch(historyPath, { recursive: false }, (eventType, filename) => {
            // Only process .md files
            if (!filename || !filename.endsWith('.md')) {
                return;
            }
            
            const srcPath = path.join(historyPath, filename);
            
            // Wait a bit for file write to complete
            setTimeout(() => {
                if (fs.existsSync(srcPath)) {
                    console.log(`[Project Backup] New file detected: ${filename}`);
                    const result = backupFileToProject(srcPath, customBasePath, projectName);
                    
                    // Update index files after backing up new file
                    if (result.success) {
                        updateAllIndexes(customBasePath);
                    }
                }
            }, 500);
        });
        
        // Attach metadata to watcher for tracking
        watcher.projectName = projectName;
        watcher.historyPath = historyPath;
        
        return watcher;
        
    } catch (error) {
        console.error(`[Project Backup] Failed to watch ${label}:`, error);
        return null;
    }
}

// ----------------------------------------------------------------------
// DEDUP/COMPARE & CLEANUP HELPERS
// Compare history with destination, ensure backup, and cleanup local
// ----------------------------------------------------------------------

/**
 * Check whether two files are byte-to-byte identical
 * @param {string} fileA
 * @param {string} fileB
 * @returns {boolean}
 */
function areFilesIdentical(fileA, fileB) {
    try {
        if (!fs.existsSync(fileA) || !fs.existsSync(fileB)) {
            return false;
        }
        const statA = fs.statSync(fileA);
        const statB = fs.statSync(fileB);
        if (statA.size !== statB.size) {
            return false;
        }
        const bufA = fs.readFileSync(fileA);
        const bufB = fs.readFileSync(fileB);
        if (bufA.length !== bufB.length) {
            return false;
        }
        // Fast path for identical references
        if (bufA === bufB) {
            return true;
        }
        for (let i = 0; i < bufA.length; i++) {
            if (bufA[i] !== bufB[i]) {
                return false;
            }
        }
        return true;
    } catch (error) {
        console.error('[Project Backup] File compare failed:', error);
        return false;
    }
}

/**
 * Determine whether all .md files in historyPath already exist with identical
 * content in both destination locations (history + project dir)
 * @param {string} historyPath
 * @param {string} customBasePath
 * @param {string} projectName
 * @returns {boolean}
 */
function isHistoryIdenticalToProject(historyPath, customBasePath, projectName) {
    try {
        if (!fs.existsSync(historyPath)) return true;
        const files = fs.readdirSync(historyPath).filter(f => f.endsWith('.md'));
        if (files.length === 0) return true;

        const historyDestDir = path.join(customBasePath, 'history');
        const projectDestDir = path.join(customBasePath, projectName);

        for (const file of files) {
            const src = path.join(historyPath, file);
            const destHistory = path.join(historyDestDir, file);
            const destProject = path.join(projectDestDir, file);
            if (!areFilesIdentical(src, destHistory) || !areFilesIdentical(src, destProject)) {
                return false;
            }
        }
        return true;
    } catch (error) {
        console.error('[Project Backup] Identity check failed:', error);
        return false;
    }
}

/**
 * Ensure destination has all files from historyPath; back up only when missing
 * or different content. Returns stats.
 * @param {string} historyPath
 * @param {string} customBasePath
 * @param {string} projectName
 * @returns {{ total: number, alreadyIdentical: number, backedUp: number, failed: number }}
 */
function ensureProjectBackupFromHistory(historyPath, customBasePath, projectName) {
    const stats = { total: 0, alreadyIdentical: 0, backedUp: 0, failed: 0 };
    try {
        if (!fs.existsSync(historyPath)) return stats;
        const files = fs.readdirSync(historyPath).filter(f => f.endsWith('.md'));
        stats.total = files.length;

        if (files.length === 0) return stats;

        const historyDestDir = path.join(customBasePath, 'history');
        const projectDestDir = path.join(customBasePath, projectName);
        if (!fs.existsSync(historyDestDir)) fs.mkdirSync(historyDestDir, { recursive: true });
        if (!fs.existsSync(projectDestDir)) fs.mkdirSync(projectDestDir, { recursive: true });

        for (const file of files) {
            const src = path.join(historyPath, file);
            const destHistory = path.join(historyDestDir, file);
            const destProject = path.join(projectDestDir, file);

            const identicalHistory = areFilesIdentical(src, destHistory);
            const identicalProject = areFilesIdentical(src, destProject);
            if (identicalHistory && identicalProject) {
                stats.alreadyIdentical++;
                continue;
            }

            const res = backupFileToProject(src, customBasePath, projectName);
            if (res && res.success) {
                stats.backedUp++;
            } else {
                stats.failed++;
            }
        }

        // Refresh indexes when there were new backups
        if (stats.backedUp > 0) {
            updateAllIndexes(customBasePath);
        }

        console.log(`[Project Backup] Ensure backup summary for ${projectName}:`, stats);
        return stats;
    } catch (error) {
        console.error('[Project Backup] Ensure backup failed:', error);
        return stats;
    }
}

/**
 * Delete the local .specstory directory based on a historyPath
 * e.g., historyPath = <workspace>/.specstory/history -> delete <workspace>/.specstory
 * @param {string} historyPath
 * @returns {boolean}
 */
function deleteLocalSpecStoryByHistoryPath(historyPath) {
    try {
        if (!historyPath) return false;
        const specstoryDir = path.dirname(historyPath); // .specstory
        if (!fs.existsSync(specstoryDir)) return true;
        fs.rmSync(specstoryDir, { recursive: true, force: true });
        console.log('[Project Backup] ✓ Deleted local .specstory:', specstoryDir);
        return true;
    } catch (error) {
        console.error('[Project Backup] Failed to delete local .specstory:', error);
        return false;
    }
}

// ----------------------------------------------------------------------
// MODULE EXPORTS
// ----------------------------------------------------------------------

module.exports = {
    extractProjectName,
    createProjectAbbreviation,
    backupFileToProject,
    backupProjectDirectory,
    watchProjectDirectory,
    generateIndexFile,
    updateAllIndexes,
    // New helpers for compare/ensure/cleanup flow
    areFilesIdentical,
    isHistoryIdenticalToProject,
    ensureProjectBackupFromHistory,
    deleteLocalSpecStoryByHistoryPath
};

console.log('[Project Backup] Project-based backup module loaded');

