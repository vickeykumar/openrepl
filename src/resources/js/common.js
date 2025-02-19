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
 * @param {string} [options.model='gpt-3.5-turbo'] - The model to use
 * @param {number} [options.temperature=0.5] - Sampling temperature
 * @param {number} [options.max_tokens=800] - Maximum number of tokens
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
        max_tokens = 800
    } = options;

    const requestBody = {
        model,
        messages: [{ role: 'user', content: prompt }],
        temperature,
        max_tokens
    };

    return fetch(baseUri, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${api_key}`
        },
        body: JSON.stringify(requestBody)
    });
}

// Save questions, ensuring a maximum of 100 entries.
function saveNewQuestions(newQuestion) {
  let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
  let exists = storedQuestions.some(q => q.nameHyphenated === newQuestion.nameHyphenated);

  if (exists) {
    return { error: "This question already exists.", storedQuestions };
  }

  storedQuestions.push(newQuestion);

  // Keep only the latest 1000 entries
  if (storedQuestions.length > 1000) {
    storedQuestions = storedQuestions.slice(-100);
  }

  localStorage.setItem('questions', JSON.stringify(storedQuestions));

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
 * @property {number} added - Timestamp of when the question was created.
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
    alert("Invalid difficulty level! Must be one of: Easy, Medium, Hard.");
    return null;
  }

  let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
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
  "code_templates": { // generate template for each language in ${language}
    "language name": {
      "template": "Provide a function signature and a main function to verify the solution.",
      "multiline_comment_start": "String for multiline comment start.",
      "multiline_comment_end": "String for multiline comment end."
    }
  }
}
\`\`\`
**Ensure that the output strictly follows the JSON format above**
`;

	// Create a loader element and add it to the page
  const loader = document.createElement("div");
	loader.classList.add("loader");

	const modal = document.getElementById("modal");
	modal?.appendChild(loader); // Appends only if modal exists

	try {
      const response = await getResponseFromOpenAI(openai_access_token, prompt, {baseUri: "/chat/completions"});

      if (!response.ok) {
          throw new Error(`API request failed with status ${response.status}: ${response.statusText}`);
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
				  return {
				      id,
				      name,
				      nameHyphenated,
				      topic,
				      difficulty: difficultyLevel,
				      description: generatedQuestion.description,
				      code_templates: generatedQuestion.code_templates,
				      added: addedEpoch,
                      delimeter: " Welcome to OpenREPL!! you can start coding here. ",
				  };
      } else {
          throw new Error("No content returned from OpenAI API.");
      }
  } catch (error) {
  		console.error("Error fetching new question:", error);
      alert("Error fetching new question, error:"+error.message);
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
async function getCodeTemplate(nameHyphenated, language) {
    // Fetch stored questions
    let storedQuestions = JSON.parse(localStorage.getItem("questions")) || [];

    // Find the question by nameHyphenated
    let question = storedQuestions.find(q => q.nameHyphenated === nameHyphenated);

    if (!question) {
        console.error("Question not found: ", nameHyphenated);
        alert("Question not found: "+nameHyphenated+", Cick on  'New Question' to Add a new question.")
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
1. The template includes a function signature. don't implement it.
2. It contains a main function that demonstrates how to call the function.
3. Use the correct comment syntax for the given language.
4. Do not repeat the prompt text in the output.
5. The response must be in **valid JSON format**.
${question.description ? '' : descriptionprompt}

### **Output Format**: // generate only json part only
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

    try {
        const response = await getResponseFromOpenAI(openai_access_token, prompt, {baseUri: "/chat/completions"});

        if (!response.ok) {
            throw new Error(`API request failed with status ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();
        if (data.choices?.length > 0 && data.choices[0].message?.content) {
        		console.log("unsanitized json: ", data.choices[0].message?.content);
            const sanitizedJSON = sanitizeJSONString(data.choices[0].message.content);

            if (!sanitizedJSON) {
                throw new Error("Invalid JSON response from OpenAI.");
            }

            console.log("sanitized json: ", sanitizedJSON);
            const generatedTemplate = JSON.parse(sanitizedJSON);

            // Ensure the response contains the expected structure
            if (!generatedTemplate[language]) {
                throw new Error(`No template found for language: ${language}`);
            }

            // Update the stored question with the new template
            question.code_templates[language] = generatedTemplate[language];
            if (!question.description) {
                // set newely generated description
                question.description = generatedTemplate.description;
            }

            // Save the updated questions list back to localStorage
            localStorage.setItem("questions", JSON.stringify(storedQuestions));

            return generatedTemplate[language];
        } else {
            throw new Error("No valid content returned from OpenAI API.");
        }
    } catch (error) {
        console.error("Error fetching code template:", error);
        return null;
    }
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
	    }
	    icon.addEventListener('click', showNav);
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
})();
