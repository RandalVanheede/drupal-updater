export namespace config {
	
	export class ProjectConfig {
	    GitStartBranch: string;
	    GitNewBranchPattern: string;
	    IgnoredPackages: string[];
	    SmokePaths: string[];
	    ClaudeInstructions: string;
	
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
	    }
	}
	export class Config {
	    BaseFolders: string[];
	    ProjectPaths: string[];
	    MaxConcurrent: number;
	    UpdateMode: string;
	    ClaudeModel: string;
	    ClaudePrompt: string;
	    ClaudeAllowedTools: string[];
	    ClaudePermissionMode: string;
	    ClaudeTimeoutMinutes: number;
	    ExcludedProjects: string[];
	    Projects: Record<string, ProjectConfig>;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.BaseFolders = source["BaseFolders"];
	        this.ProjectPaths = source["ProjectPaths"];
	        this.MaxConcurrent = source["MaxConcurrent"];
	        this.UpdateMode = source["UpdateMode"];
	        this.ClaudeModel = source["ClaudeModel"];
	        this.ClaudePrompt = source["ClaudePrompt"];
	        this.ClaudeAllowedTools = source["ClaudeAllowedTools"];
	        this.ClaudePermissionMode = source["ClaudePermissionMode"];
	        this.ClaudeTimeoutMinutes = source["ClaudeTimeoutMinutes"];
	        this.ExcludedProjects = source["ExcludedProjects"];
	        this.Projects = this.convertValues(source["Projects"], ProjectConfig, true);
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

export namespace main {
	
	export class ProjectInfo {
	    name: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
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

