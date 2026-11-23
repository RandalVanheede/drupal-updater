export namespace config {
	
	export class ProjectConfig {
	    GitStartBranch: string;
	    GitNewBranchPattern: string;
	    IgnoredPackages: string[];
	    SmokePaths: string[];
	    ClaudeInstructions: string;
	    GitMergeTarget: string;
	    GitCommitMessage: string;
	    DashboardProject: string;
	    Multisite: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.GitStartBranch = source["GitStartBranch"];
	        this.GitNewBranchPattern = source["GitNewBranchPattern"];
	        this.IgnoredPackages = source["IgnoredPackages"];
	        this.SmokePaths = source["SmokePaths"];
	        this.ClaudeInstructions = source["ClaudeInstructions"];
	        this.GitMergeTarget = source["GitMergeTarget"];
	        this.GitCommitMessage = source["GitCommitMessage"];
	        this.DashboardProject = source["DashboardProject"];
	        this.Multisite = source["Multisite"];
	    }
	}
	export class Config {
	    BaseFolders: string[];
	    ScanDepth: number;
	    ProjectPaths: string[];
	    MaxConcurrent: number;
	    UpdateMode: string;
	    ClaudeModel: string;
	    ClaudePrompt: string;
	    ClaudePromptBase: string;
	    ClaudeAllowedTools: string[];
	    ClaudePermissionMode: string;
	    ClaudeTimeoutMinutes: number;
	    ExcludedProjects: string[];
	    Defaults: ProjectConfig;
	    Projects: Record<string, ProjectConfig>;
	    DashboardURL: string;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.BaseFolders = source["BaseFolders"];
	        this.ScanDepth = source["ScanDepth"];
	        this.ProjectPaths = source["ProjectPaths"];
	        this.MaxConcurrent = source["MaxConcurrent"];
	        this.UpdateMode = source["UpdateMode"];
	        this.ClaudeModel = source["ClaudeModel"];
	        this.ClaudePrompt = source["ClaudePrompt"];
	        this.ClaudePromptBase = source["ClaudePromptBase"];
	        this.ClaudeAllowedTools = source["ClaudeAllowedTools"];
	        this.ClaudePermissionMode = source["ClaudePermissionMode"];
	        this.ClaudeTimeoutMinutes = source["ClaudeTimeoutMinutes"];
	        this.ExcludedProjects = source["ExcludedProjects"];
	        this.Defaults = this.convertValues(source["Defaults"], ProjectConfig);
	        this.Projects = this.convertValues(source["Projects"], ProjectConfig, true);
	        this.DashboardURL = source["DashboardURL"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace coreupdates {
	
	export class Refresh {
	    version: string;
	    fetchFailed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Refresh(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.fetchFailed = source["fetchFailed"];
	    }
	}

}

export namespace main {
	
	export class UnmatchedProject {
	    local: string;
	    suggestions: DashRef[];
	
	    static createFrom(source: any = {}) {
	        return new UnmatchedProject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.local = source["local"];
	        this.suggestions = this.convertValues(source["suggestions"], DashRef);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DashRef {
	    ref: string;
	    name: string;
	    customer: string;
	
	    static createFrom(source: any = {}) {
	        return new DashRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ref = source["ref"];
	        this.name = source["name"];
	        this.customer = source["customer"];
	    }
	}
	export class DashCandidates {
	    projects: DashRef[];
	    unmatched: UnmatchedProject[];
	
	    static createFrom(source: any = {}) {
	        return new DashCandidates(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projects = this.convertValues(source["projects"], DashRef);
	        this.unmatched = this.convertValues(source["unmatched"], UnmatchedProject);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class DashboardState {
	    configured: boolean;
	    error?: string;
	    signedIn: boolean;
	    server: string;
	
	    static createFrom(source: any = {}) {
	        return new DashboardState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.error = source["error"];
	        this.signedIn = source["signedIn"];
	        this.server = source["server"];
	    }
	}
	export class ProjectInfo {
	    name: string;
	    path: string;
	    repoFile: boolean;
	    core: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.repoFile = source["repoFile"];
	        this.core = source["core"];
	    }
	}
	export class PromptMerge {
	    mine: string;
	    theirs: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new PromptMerge(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mine = source["mine"];
	        this.theirs = source["theirs"];
	        this.source = source["source"];
	    }
	}
	
	export class UpdateRequest {
	    projects: string[];
	
	    static createFrom(source: any = {}) {
	        return new UpdateRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projects = source["projects"];
	    }
	}

}

export namespace updater {
	
	export class Release {
	    version: string;
	    url: string;
	    canInstall: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Release(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.url = source["url"];
	        this.canInstall = source["canInstall"];
	    }
	}

}

