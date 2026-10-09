// utilities
var get = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelector(selector);
};

var getAll = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelectorAll(selector);
};

var globaltemperature = localStorage.getItem("temperature");
globaltemperature = isNaN(parseFloat(globaltemperature)) ? 0.3 : parseFloat(globaltemperature);

var QUESTIONS_KEY = 'questions';

// The New question dialog's Model and Effort fields (initQuestionModelFields,
// below). Registered here, ahead of the Firebase setup, because the home page
// does not load Firestore and this script stops at firebase.firestore() there.
document.addEventListener("DOMContentLoaded", initQuestionModelFields);
window.addEventListener("load", initQuestionModelFields);

// Firebase handles. The home page loads Firebase without Firestore (practice
// sync is on the practice pages), and firebase.firestore() then throws; that
// must not stop the rest of this script, which also runs the menu button, so
// the setup is guarded and the handles stay null where there is no Firestore.
var firestoredb = null;
var firebaseAuth = null;
var currentUserID = null;
var batch = null;
function initFirebaseHandles() {
  try {
    if (firebase.apps.length === 0) {
      firebase.initializeApp(firebaseconfig);
    }
    firestoredb = firebase.firestore(); // Initialize Firestore
    firebaseAuth = firebase.auth(); // Initialize Firebase Auth
    batch = firestoredb.batch();        // Create a Firestore batch
  } catch (e) {
    console.warn("Firestore is not available on this page, so practice questions are not synced here:", e.message);
  }
}
initFirebaseHandles();

  function getQuestionDocRef(docName) {
    if (!currentUserID) {
      return null
    }
    return firestoredb.collection(`users/${currentUserID}/questions`).doc(docName);
  }

  function updatequestiondb(docName, question) {
    const docRef = getQuestionDocRef(docName)
    if (docRef) {
      console.log("updated: ", question);
      batch.set(docRef, question, { merge: true });
    }
  }

  function deletequestiondb(docName) {
    const docRef = getQuestionDocRef(docName)
    if (docRef) {
      batch.delete(docRef);
      commitFirestoreBatch();
    }
  }

  function commitFirestoreBatch() {
    if (!batch) return; // no Firestore on this page
    batch.commit()
    .then(() => {
      console.log("Sync complete!");
      batch = firestoredb.batch();
    })
    .catch((error) => {
      console.error("Batch commit failed:", error);
    });
  }

const topics = [
    "Two Pointers",
    "Hash Maps and Sets",
    "Linked Lists",
    "Fast and Slow Pointers",
    "Sliding Windows",
    "Binary Search",
    "Stacks",
    "Heaps",
    "Intervals",
    "Prefix Sums",
    "Trees",
    "Tries",
    "Graphs",
    "Recursion",
    "Backtracking",
    "Dynamic Programming",
    "Greedy",
    "Sort and Search",
    "Bit Manipulation",
    "Math and Geometry",
    "Divide and Conquer",         // Essential for algorithms like merge sort, quicksort
    "String Manipulation",        // Covers regex, pattern matching, etc.
    "Combinatorics",              // Helps in counting and probability problems
    "Game Theory",                // Useful for AI-based problems and decision making
    "Number Theory",              // Covers prime numbers, GCD/LCM, modular arithmetic
    "Network Flow",               // Advanced graph algorithm topic
    "Topological Sorting",        // Used in scheduling problems and dependency resolution
    "Fenwick Trees & Segment Trees", // Advanced data structures
    "Union-Find & Disjoint Sets", // Used in Kruskal’s algorithm and connectivity problems
    "Monotonic Stack/Queue",      // Used in problems like Next Greater Element
    "Reservoir Sampling",         // Used for sampling large streams of data
    "System Design",
    "Operating Systems",
    "Databases",
    "Concurrency and Multithreading",
    "Networking",
    "Compilers and Interpreters",
    "Memory Management",
    "Cryptography and Security",
    "Artificial Intelligence & Machine Learning"
    ];

/**
 * Extracts and sanitizes the JSON string from extra text before/after it.
 * @param {string} text - The raw text containing JSON and possibly extra text.
 * @returns {string|null} - The extracted JSON string or null if not found.
 */
function sanitizeJSONString(text) {
    if (!text) return null;

    // Find the first occurrence of '{' and last occurrence of '}'
    const startIndex = text.indexOf('{');
    const endIndex = text.lastIndexOf('}');

    if (startIndex !== -1 && endIndex !== -1 && startIndex < endIndex) {
        return text.substring(startIndex, endIndex + 1); // Extract valid JSON part
    }

    return null; // Return null if no valid JSON is found
}

/**
 * Get the response from OpenAI API
 * @param {string} api_key - The API key (mandatory)
 * @param {string} prompt - The prompt to send to OpenAI
 * @param {object} options - Optional parameters
 * @param {string} [options.baseUri='https://api.openai.com/v1/chat/completions'] - The OpenAI API endpoint
 * @param {string} [options.model='gpt-4o-mini'] - The model to use
 * @param {number} [options.temperature=0.5] - Sampling temperature
 * @param {number} [options.max_tokens=800] - Maximum number of tokens
 * @param {object} [options.fields] - Model, effort and token settings to send as they are (replaces model, temperature and max_tokens)
 * @param {object} [options.response_format] - For example {type: "json_object"}
 * @returns {Promise<Response>} - The API response as a Promise
 */
async function getResponseFromOpenAI(api_key, prompt, options = {}) {
    if (!api_key) {
        throw new Error("API key is required");
    }

    const {
        baseUri = 'https://api.openai.com/v1/chat/completions',
        model = 'gpt-4o-mini',
        temperature = globaltemperature,
        max_tokens = 800,
        fields = null,          // the model and its own settings, from questionRequestFields()
        response_format = null, // {type: "json_object"} asks for a reply that is valid JSON
        signal = null           // an AbortSignal: the request is dropped when it fires
    } = options;

    const messages = [{ role: 'user', content: prompt }];
    const requestBody = fields
        ? { ...fields, messages }
        : { model, messages, temperature, max_tokens };
    if (response_format) {
        requestBody.response_format = response_format;
    }

    return fetch(baseUri, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${api_key}`
        },
        body: JSON.stringify(requestBody),
        signal: signal || undefined
    });
}

// The model and effort a generated question is made with: the same choice as
// Genie's (the chip in its panel), kept by js/model-choice.js. A question is
// long (a description and a template per language), so it gets more room than a
// chat answer.
function questionRequestFields() {
    const choice = window.ModelChoice;
    if (choice) {
        return choice.fields({ extraTokens: 3000, maxTokens: 3000, temperature: globaltemperature });
    }
    return { model: 'gpt-4o-mini', temperature: globaltemperature, max_tokens: 3000 };
}

// Asks for a JSON reply. JSON mode keeps a quote or a newline inside the text
// from breaking the reply; if the API refuses it (400), the same request is
// sent once more without it and sanitizeJSONString tidies what comes back.
async function requestJSONFromOpenAI(prompt, signal) {
    const fields = questionRequestFields();
    let response = await getResponseFromOpenAI(openai_access_token, prompt, {
        baseUri: "/chat/completions", fields, response_format: { type: "json_object" }, signal
    });
    if (response.status === 400) {
        response = await getResponseFromOpenAI(openai_access_token, prompt, { baseUri: "/chat/completions", fields, signal });
    }
    return response;
}

// The error for a reply that is not ok. When the chosen model is not reachable
// (the proxy answers type "model_unavailable", for Gemma through OpenRouter) it
// says so and what to do, instead of a status code.
//
// The Error also says why, for js/question-loader.js: .kind ("limit" for 429,
// "auth" for 401 and 403, "off" when Genie or the model is switched off,
// otherwise "server"), .status and .serverMessage (what the proxy wrote).
async function apiFailure(response) {
    let serverMessage = "", type = "", code = "";
    try {
        const body = await response.clone().json();
        if (body && body.error) {
            serverMessage = String(body.error.message || "");
            type = String(body.error.type || "");
            code = String(body.error.code || "");
        }
    } catch (e) {
        // not JSON: the status line below
    }
    let err;
    if (type === "model_unavailable") {
        err = new Error(serverMessage + (code === "model_disabled"
            ? ". Choose another model."
            : ". Try again in a moment, or choose another model."));
        err.kind = "off";
    } else {
        err = new Error(`API request failed with status ${response.status}: ${response.statusText}`);
        err.kind = response.status === 429 ? "limit"
            : (response.status === 401 || response.status === 403) ? "auth"
            : (response.status === 503 && type === "GenieDisabled") ? "off"
            : "server";
    }
    err.status = response.status;
    err.serverMessage = serverMessage;
    return err;
}

// The Model and Effort fields of the New question dialog, on the home page and
// on the practice page: filled from js/model-choice.js's models (in a group
// for each provider) and four efforts, and kept in step with Genie's choice. Safe to call more than once.
function initQuestionModelFields() {
    const modelSel = document.getElementById("qmodel");
    const effortSel = document.getElementById("qeffort");
    const choice = window.ModelChoice;
    if (!modelSel || !effortSel || !choice || modelSel.dataset.ready) return;
    modelSel.dataset.ready = "true";

    const temperature = document.getElementById("temperature");
    const temperatureHint = document.getElementById("temperature-hint");
    const hint = document.getElementById("qmodel-hint");
    const creativityText = temperatureHint ? temperatureHint.textContent : "";

    // one group for each provider: "OpenAI", "OpenRouter"
    const groups = {};
    choice.models.forEach(m => {
        if (!groups[m.group]) {
            groups[m.group] = document.createElement("optgroup");
            groups[m.group].label = m.group;
            modelSel.appendChild(groups[m.group]);
        }
        groups[m.group].appendChild(new Option(m.name, m.id));
    });
    choice.efforts.forEach(e => effortSel.add(new Option(e.label, e.id)));

    function show() {
        const now = choice.get();
        const model = choice.models.find(m => m.id === now.model) || choice.models[0];
        modelSel.value = model.id;
        effortSel.value = now.effort;
        effortSel.disabled = !model.reasoning;
        // creativity is a temperature, which Luna does not take
        if (temperature) temperature.disabled = model.reasoning;
        if (temperatureHint) {
            temperatureHint.textContent = model.reasoning
                ? "Creativity doesn't apply to " + model.name + "."
                : creativityText;
        }
        if (hint) {
            const effort = choice.efforts.find(e => e.id === now.effort);
            hint.textContent = model.reasoning
                ? model.name + " thinks before it writes. " + (effort ? effort.note : "")
                : model.name + " writes right away, so effort doesn't apply.";
        }
    }
    modelSel.addEventListener("change", () => { choice.set(modelSel.value, undefined); show(); });
    effortSel.addEventListener("change", () => { choice.set(undefined, effortSel.value); show(); });
    // Genie's chip changed it (or another tab did)
    choice.onChange(show);
    show();
}

// Save questions, ensuring a maximum of 100 entries.
function saveNewQuestions(newQuestion) {
  // PracticeStore (js/practice-store.js) keeps the list and syncs it to the account (T17)
  if (window.PracticeStore) return PracticeStore.add(newQuestion);
  let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
  let exists = storedQuestions.some(q => q.nameHyphenated === newQuestion.nameHyphenated);

  if (exists) {
    return { error: "This question already exists.", storedQuestions };
  }
  // Track questions before adding new ones
  const previousQuestionNames = new Set(storedQuestions.map(q => q.nameHyphenated));
  storedQuestions.push(newQuestion);

  // Keep only the latest 1000 entries
  if (storedQuestions.length > 1000) {
    storedQuestions = storedQuestions.slice(-1000);
  }

  localStorage.setItem(QUESTIONS_KEY, JSON.stringify(storedQuestions));

   updatequestiondb(newQuestion.nameHyphenated, newQuestion);

  // Calculate deleted questions
  const newQuestionNames = new Set(storedQuestions.map(q => q.nameHyphenated));
  const deletedQuestions = [...previousQuestionNames].filter(name => !newQuestionNames.has(name));

  // Delete questions from Firestore
  deletedQuestions.forEach(qname => deletequestiondb(qname));

  return { error: null, storedQuestions };
}

/**
 * @typedef {Object} Question
 * @property {string} id - Unique identifier (timestamp-based).
 * @property {string} name - The generated question name.
 * @property {string} nameHyphenated - Hyphenated version of the question name for URLs.
 * @property {string} topic - The topic of the question.
 * @property {"Easy" | "Medium" | "Hard"} difficulty - The difficulty level.
 * @property {string} language - The programming language.
 * @property {number} updated - Timestamp of when the question was last updated.
 * @property {string} delimeter - Delimeter string that separates problem description section to code.
 */

/**
 * Generates a new question object.
 * @param {string} topic - The topic of the question.
 * @param {"Easy" | "Medium" | "Hard"} difficultyLevel - The difficulty level.
 * @param {string} [language="General"] - The programming language (default is "General").
 * @returns {Question | null} The new question object if valid, otherwise null.
 */
async function generateNewQuestion(topic, difficultyLevel, customPrompt, language = "c,cpp,go,python") {
  let israndomtopic = false;
  // Validate topic and difficultyLevel
  if (!topic) {
    topic = "Any one Random topic form "+topics.slice(0, -1).join(", ");
    israndomtopic = true;
  }
  if (!["Easy", "Medium", "Hard"].includes(difficultyLevel)) {
    notify("Choose Easy, Medium or Hard.", { type: "error", title: "Invalid difficulty" });
    return null;
  }

  let storedQuestions = JSON.parse(localStorage.getItem(QUESTIONS_KEY)) || [];
  // Extract the list of previous question names
	let previousTitles = storedQuestions.filter(q => q.topic === topic).map(q => q.name).join(", ");

	const prompt = `Generate a unique data structure and algorithm coding question based on these criteria:
    
- **Topic:** ${topic}  
- **Difficulty Level:** ${difficultyLevel}  
- **Programming Languages:** ${language}  

### **Question Requirements**:
1. The question should be a real-world problem related to the given topic.
2. The problem should have a **clear problem statement** with necessary constraints.
3. **Do not explicitly mention the topic** in the title or description. The user should figure it out after reading.
4. Strictly Format the description so that **no line exceeds 100 characters** for better readability.
5. Use **stick figure drawings** whenever necessary to visually explain the problem.
6. Provide at least **two sample test cases** in the question description.
7. Ensure the problem is suitable for implementation in these languages(comma separated): ${language}. 
8. **Do NOT generate a question with any of these already generated questions(comma separated):**  
   ${previousTitles ? previousTitles : ""}
${customPrompt ? customPrompt : ""}

### **Output Format**:
\`\`\`json
{
  "title": "Title of the problem",
  "description": "Detailed problem description with sample test cases...",
  "code_templates": {
    "language name": {
      "template": "Provide a function signature and a main function to verify the solution.",
      "multiline_comment_start": "String for multiline comment start.",
      "multiline_comment_end": "String for multiline comment end."
    }
  }
}
\`\`\`
**Ensure that the output strictly follows the JSON format above.** It must be one valid JSON object with no comments, every quote and newline inside a string escaped, and one entry in "code_templates" for each of these languages: ${language}.
`;

	// Create a loader element and add it to the page
  const loader = document.createElement("div");
	loader.classList.add("loader");

	const modal = document.getElementById("modal");
	modal?.appendChild(loader); // Appends only if modal exists

	try {
      const response = await requestJSONFromOpenAI(prompt);

      if (!response.ok) {
          throw await apiFailure(response);
      }

      const data = await response.json();

      if (data.choices?.length > 0 && data.choices[0].message?.content) {
      		const sanitizedJSON = sanitizeJSONString(data.choices[0].message.content);
      		if (!sanitizedJSON) {
      			throw new Error("invalid response json");
      		}
          const generatedQuestion = JSON.parse(sanitizedJSON); // Parse the response as JSON
          const name = generatedQuestion.title.trim();
          // Generate a unique ID based on the current timestamp
				  let addedEpoch = Date.now();
				  let nameHyphenated = name.toLowerCase().replace(/\s+/g, "-");
				  let id = addedEpoch.toString(); // Unique ID
                  if (israndomtopic) {
                    topic = "Random Topic";
                  }
          // Create new question object and return
				  return  {
				      id,
				      name,
				      nameHyphenated,
				      topic,
				      difficulty: difficultyLevel,
				      description: generatedQuestion.description,
				      code_templates: generatedQuestion.code_templates,
				      updated: addedEpoch,
                      delimeter: " Welcome to OpenREPL!! you can start coding here. ",
				  };
      } else {
          throw new Error("No content returned from OpenAI API.");
      }
  } catch (error) {
  		console.error("Error fetching new question:", error);
      notify(error.message, { type: "error", title: "Couldn't fetch a new question" });
      return null;
  } finally {
      // Remove the loader after completion (success or failure)
      loader.remove();
  }
}

/**
 * Retrieves a code template for a given problem and language.
 * @param {string} nameHyphenated - The hyphenated name of the problem.
 * @param {string} language - The programming language.
 * @returns {Object|null} The code template for the given language or null if not found.
 */
async function getCodeTemplate(nameHyphenated, language, options = {}) {
    // Fetch stored questions
    let storedQuestions = JSON.parse(localStorage.getItem(QUESTIONS_KEY)) || [];

    // Find the question by nameHyphenated
    let question = storedQuestions.find(q => q.nameHyphenated === nameHyphenated);

    if (!question) {
        console.error("Question not found: ", nameHyphenated);
        notify("Click New question to create one.", { type: "info", title: "Question not found" })
        return null;
    }

    // Check if the code template already exists for the given language
    if (question.code_templates && question.code_templates[language]) {
        return question.code_templates[language];
    }

    const descriptionprompt = `- Each line in description is wrapped to a maximum of 100 characters, breaking at word boundaries( use \\n).
- The problem description should explain the requirements and constraints in detail.
- Use **stick figure drawings** whenever necessary to visually explain the problem.
- Provide at least two sample test cases, formatted using \\n as separator.`;
    // Construct OpenAI prompt
    const prompt = `Generate a code template for solving the following problem:

### **Problem Title**: ${question.name}

${question.description ? `### **Problem Description**:\n${question.description}\n` : ''}

### **Output Format**:
The output should be a valid JSON object containing a code template for **${language}**.

Ensure that:
1. The template includes an unimplemented function signature with a main function to test it .
2. test code should contain expected and actual output to call and test above function.
3. Use the correct comment syntax for the given language.
4. Do not repeat the prompt text in the output.
5. The response must be in **valid JSON format**.
${question.description ? '' : descriptionprompt}

### **Output Format**: (the JSON object only, with no comments)
\`\`\`json
{
  ${question.description ? '' : '"description": "Detailed problem description with sample test cases ...",'}
  "${language}": {
    "template": "<function signature/code template here>",
    "multiline_comment_start": "<start comment syntax>",
    "multiline_comment_end": "<end comment syntax>"
  }
}
\`\`\`
**Ensure that the output strictly follows the JSON format above, with "${language}" as the key.**
`;

    // Whatever goes wrong is thrown with .kind, so that the caller can say why
    // (js/question-loader.js); an abort (a cancel, the time limit) passes through.
    let response;
    try {
        response = await requestJSONFromOpenAI(prompt, options.signal);
    } catch (error) {
        if (error && error.name === "AbortError") throw error;
        const unreachable = new Error("Couldn't reach the server.");
        unreachable.kind = "offline";
        throw unreachable;
    }
    if (!response.ok) {
        throw await apiFailure(response);
    }

    try {
        const data = await response.json();
        if (!(data.choices?.length > 0 && data.choices[0].message?.content)) {
            throw new Error("No valid content returned from OpenAI API.");
        }
        console.log("unsanitized json: ", data.choices[0].message?.content);
        const sanitizedJSON = sanitizeJSONString(data.choices[0].message.content);

        if (!sanitizedJSON) {
            throw new Error("Invalid JSON response from OpenAI.");
        }

        console.log("sanitized json: ", sanitizedJSON);
        const generatedTemplate = JSON.parse(sanitizedJSON);

        // Ensure the response contains the expected structure (the language's key may be written
        // another way, or sit one level down: js/question-loader.js reads those too)
        const template = window.QuestionLoader
            ? QuestionLoader.pickTemplate(generatedTemplate, language)
            : (generatedTemplate[language] || null);
        if (!template) {
            const keys = generatedTemplate && typeof generatedTemplate === "object" ? Object.keys(generatedTemplate).join(", ") : typeof generatedTemplate;
            throw new Error(`No template found for language: ${language} (the answer had: ${keys || "nothing"})`);
        }

        // Update the stored question with the new template
        question.code_templates[language] = template;
        // the description came with it, for a question that had none yet
        if (!question.description && generatedTemplate.description) question.description = generatedTemplate.description;

        // Save the updated question back
        if (window.PracticeStore) PracticeStore.update(question);
        else localStorage.setItem("questions", JSON.stringify(storedQuestions));

        return template;
    } catch (error) {
        if (error && error.name === "AbortError") throw error;
        console.error("Error reading the code template:", error);
        error.kind = "format";
        throw error;
    }
}


// Function to fetch login data and update QUESTIONS_KEY
function getUserLogin() {
    return new Promise(async (resolve, reject) => {
        try {
            const response = await fetch('/login');
            if (!response.ok) {
                throw new Error('Network response was not ok');
            }

            const data = await response.json();

            resolve(data);
        } catch (error) {
            console.error('Failed to fetch login status:', error);
            reject(error); // Reject promise on error
        }
    });
}

// common App
(function commonApp() {
	// body...
	var topNav = get('.menu');
	var icon = get('.toggle');

	window.addEventListener('load', function(){
	    function showNav() {
	      if (topNav.className === 'menu') {
	        topNav.className += ' responsive';
	        icon.className += ' open';
	      } else {
	        topNav.className = 'menu';
	        icon.classList.remove('open');
	      }
	      var btn = icon.querySelector('button') || icon;
	      btn.setAttribute('aria-expanded', icon.classList.contains('open') ? 'true' : 'false');
	    }
	    icon.addEventListener('click', showNav);
	    // the menu toggle holds a real <button> (T15), so Enter and Space work through its click
	    //replace all urls with with origin url in case of iframe webredirect
	    var parent_origin = '';
	    if (document.referrer !== '') {
	    	parent_origin = new URL(document.referrer).origin;
	    }
	    if(window.self !== window.top && parent_origin !==window.location.origin) {
	    	//inside an iframe web redirect
	    	var links = document.links;
		let i = links.length;
		while (i--) {
			let absUrl = new URL(links[i].href, window.location.href).href;
			links[i].href = absUrl.replace(window.location.origin,parent_origin);
		}
	    }
	    
	});

    initFirebaseHandles();
})();
